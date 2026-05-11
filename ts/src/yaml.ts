/* Copyright (c) 2021-2025 Richard Rodger, MIT License */


import {
  Jsonic,
  Rule,
  RuleSpec,
  Plugin,
  Config,
  Options,
  Context,
  Lex,
  Token,
} from 'jsonic'


type YamlOptions = {
  // When true, parse() returns { meta, content } instead of bare content.
  // - meta is a per-document object {directives, explicit, ended} for single
  //   docs, or an array of such objects for multi-doc streams.
  // - content is the same value/array the no-meta path returns.
  meta?: boolean
}

type DocMeta = {
  directives: string[]
  explicit: boolean
  ended: boolean
}


// --- BEGIN EMBEDDED yaml-grammar.jsonic ---
const grammarText = `
# YAML Grammar Definition
# Parsed by a standard Jsonic instance and passed to jsonic.grammar()
# Function references (@ prefixed) are resolved against the refs map.
# State handlers (bo/ao/bc/ac) remain wired in code, since they use
# closures over per-parse state (anchors, pendingAnchors, etc.).

{
  # Amend val rule: YAML indent/element-marker handling.
  rule: val: open: {
    alts: [
      # Doc-frame markers between docs mean an empty value here; back up so
      # the stream rule consumes the marker and starts the next document.
      { s: '#DS' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DE' b: 1 a: '@val-set-null' g: yaml }
      { s: '#DR' b: 1 a: '@val-set-null' g: yaml }
      # Indent followed by content: push indent rule.
      { s: '#IN' c: '@val-indent-deeper' p: indent a: '@val-set-in-from-o0' g: yaml }
      # Same indent followed by element marker: list value at map level.
      { s: ['#IN' '#EL'] c: '@val-indent-eq-parent' p: yamlBlockList a: '@val-set-in-from-o0' g: yaml }
      # End of input means empty value.
      { s: '#ZZ' b: 1 a: '@val-set-null' g: yaml }
      # Same or lesser indent after a colon means empty value — backtrack.
      { s: '#IN' b: 1 u: { yamlEmpty: true } g: yaml }
      # This value is a list.
      { s: '#EL' p: yamlBlockList a: '@val-set-el-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: val: close: {
    alts: [
      # Doc-frame markers terminate val; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # Indent rule: start for block content at a given indent.
  rule: indent: open: [
    # Key pair => map.
    { s: ['#KEY' '#CL'] p: map b: 2 g: yaml }
    # Element marker => list.
    { s: '#EL' p: list g: yaml }
    # Plain value after indent (for nested scalars).
    { s: '#KEY' a: '@indent-plain-value' g: yaml }
  ]

  # YAML block list: handles "- " sequences without consuming "[".
  rule: yamlBlockList: open: [
    # Element value is a key-value map: - key: val
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    # Default: push to val for the element's value.
    { p: val g: yaml }
  ]
  rule: yamlBlockList: close: [
    # Doc-frame markers terminate list; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    # Indent followed by element marker: next element at same level.
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: yaml }
    # Same or lesser indent: close list.
    { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
    # Element marker at top level (no preceding newline).
    { s: '#EL' r: yamlBlockElem g: yaml }
    { s: '#ZZ' b: 1 g: yaml }
  ]

  # Subsequent elements in a yamlBlockList (via rotation).
  rule: yamlBlockElem: open: [
    { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    { p: val g: yaml }
  ]
  rule: yamlBlockElem: close: [
    # Doc-frame markers terminate elem; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: ['#IN' '#EL'] c: '@t0-eq-in' r: yamlBlockElem g: yaml }
    { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
    { s: '#EL' r: yamlBlockElem g: yaml }
    { s: '#ZZ' b: 1 g: yaml }
  ]

  # Amend list rule: close on dedent or same-indent non-element.
  rule: list: close: {
    alts: [
      # Doc-frame markers terminate list; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-le-in' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # Amend map rule: same-indent indent continues map with pair.
  rule: map: open: {
    alts: [
      { s: '#IN' c: '@o0-eq-in' r: pair g: yaml }
    ]
    inject: { append: false }
  }
  rule: map: close: {
    alts: [
      # Doc-frame markers terminate map; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # Amend pair rule: end of input ends pair; dedent closes, same-indent repeats.
  # Also handle YAML flow-mapping shapes Jsonic doesn't have natively:
  # - implicit null values: {a, b: c}  — KEY followed directly by CA or CB
  # - explicit-key marker:  {? k : v}  — leading #QM is consumed
  rule: pair: open: {
    alts: [
      { s: ['#KEY' '#CA'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#KEY' '#CB'] a: '@implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: val u: { pair: true } a: '@qm-pairkey' g: yaml }
      { s: ['#QM' '#KEY' '#CA'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: ['#QM' '#KEY' '#CB'] a: '@qm-implicit-null-pair' b: 1 g: yaml }
      { s: '#ZZ' b: 1 g: yaml }
    ]
    inject: { append: false }
  }
  rule: pair: close: {
    alts: [
      # Doc-frame markers terminate pair; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: '#IN' c: '@t0-eq-in' r: pair g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
    ]
    inject: { append: false }
  }

  # yamlElemMap: "- key: val" patterns.
  rule: yamlElemMap: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemMap: close: [
    # Doc-frame markers terminate elem-map; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: yaml }
    { s: '#IN' b: 1 g: yaml }
    { s: '#CA' b: 1 g: yaml }
    { s: '#CS' b: 1 g: yaml }
    { s: '#CB' b: 1 g: yaml }
    { s: '#ZZ' g: yaml }
  ]

  # Additional pairs in a yamlElemMap.
  rule: yamlElemPair: open: [
    { s: ['#KEY' '#CL'] p: val a: '@elem-key' g: yaml }
  ]
  rule: yamlElemPair: close: [
    # Doc-frame markers terminate elem-pair; back up for the stream rule.
    { s: '#DS' b: 1 g: yaml }
    { s: '#DE' b: 1 g: yaml }
    { s: '#DR' b: 1 g: yaml }
    { s: '#IN' c: '@t0-eq-map-in' r: yamlElemPair g: yaml }
    { s: '#IN' b: 1 g: yaml }
    { s: '#CA' b: 1 g: yaml }
    { s: '#CS' b: 1 g: yaml }
    { s: '#CB' b: 1 g: yaml }
    { s: '#ZZ' g: yaml }
  ]

  # Amend elem rule for YAML sequences ("- key: val" at top level of [ ... ]).
  # Also handle flow-sequence explicit-key entries: [? k : v] is a single-pair
  # map element. Eat the leading #QM, then back up KEY+CL so yamlElemMap
  # consumes them as a normal pair.
  rule: elem: open: {
    alts: [
      { s: ['#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
      { s: ['#QM' '#KEY' '#CL'] p: yamlElemMap b: 2 a: '@set-map-in' g: yaml }
    ]
    inject: { append: false }
  }
  rule: elem: close: {
    alts: [
      # Doc-frame markers terminate elem; back up for the stream rule.
      { s: '#DS' b: 1 g: yaml }
      { s: '#DE' b: 1 g: yaml }
      { s: '#DR' b: 1 g: yaml }
      { s: ['#IN' '#EL'] c: '@t0-eq-in' r: elem g: yaml }
      { s: '#IN' c: '@t0-eq-in' b: 1 g: yaml }
      { s: '#IN' c: '@t0-lt-in' b: 1 g: yaml }
      { s: '#EL' r: elem g: yaml }
    ]
    inject: { append: false }
  }
}
`
// --- END EMBEDDED yaml-grammar.jsonic ---


const Yaml: Plugin = (jsonic: Jsonic, options: YamlOptions) => {
  // Guard against re-entry during options() re-application.
  if ((jsonic as any).__yamlInstalled) return
  ;(jsonic as any).__yamlInstalled = true

  let TX = jsonic.token.TX
  let NR = jsonic.token.NR
  let ST = jsonic.token.ST
  let VL = jsonic.token.VL
  let CL = jsonic.token.CL
  let ZZ = jsonic.token.ZZ

  let IN = jsonic.token('#IN')

  // Shared anchor storage for the plugin instance.
  let anchors: Record<string, any> = {}
  let pendingAnchors: { name: string, inline: boolean }[] = []
  let pendingExplicitCL = false
  // Flag to tell the number matcher to skip, so text.check handles the value.
  let skipNumberMatch = false
  // Queue for tokens that need to be emitted across multiple lex calls.
  let pendingTokens: any[] = []
  // TAG directive handle mappings (e.g. %TAG !! tag:example.com/).
  // When !! is redefined, built-in type conversion is skipped.
  let tagHandles: Record<string, string> = {}
  // Per-parse accumulators for the stream rule. Reset on first lex call.
  let yamlStreamDocs: any[] = []
  let yamlStreamMeta: DocMeta[] = []
  let yamlStreamCurMeta: DocMeta | null = null
  // Incremental flow-depth cache for text.check (avoids O(n²) rescan).
  let _flowDepth = 0
  let _flowScanPos = 0
  // Persistent quote state so multi-call scans handle quotes spanning slices.
  let _inSingleQuote = false
  let _inDoubleQuote = false

  // Bring _flowDepth up to date with `upTo` by scanning lex.src incrementally.
  // Skips quoted regions so embedded brackets don't mis-count the flow depth.
  function updateFlowState(src: string, upTo: number) {
    if (upTo < _flowScanPos) {
      _flowDepth = 0; _flowScanPos = 0
      _inSingleQuote = false; _inDoubleQuote = false
    }
    for (let fi = _flowScanPos; fi < upTo; fi++) {
      let fc = src[fi]
      if (_inDoubleQuote) {
        if (fc === '\\') fi++
        else if (fc === '"') _inDoubleQuote = false
        continue
      }
      if (_inSingleQuote) {
        if (fc === "'") {
          if (src[fi + 1] === "'") fi++
          else _inSingleQuote = false
        }
        continue
      }
      if (fc === '{' || fc === '[') _flowDepth++
      else if (fc === '}' || fc === ']') { if (_flowDepth > 0) _flowDepth-- }
      else if (fc === '"') { _inDoubleQuote = true }
      else if (fc === "'") {
        let pc = fi > 0 ? src.charCodeAt(fi - 1) : 0
        if (!((pc >= 65 && pc <= 90) || (pc >= 97 && pc <= 122) || (pc >= 48 && pc <= 57))) {
          _inSingleQuote = true
        }
      }
    }
    _flowScanPos = upTo
  }


  jsonic.options({
    fixed: {
      token: {
        // Single colon is not a YAML token, so remove.
        '#CL': null,
      }
    },

    // Colons can still end unquoted text (TX, lexer.textMatcher).
    ender: ':',

    // Remove all jsonic string chars — YAML handles quotes in yamlMatcher.
    // Backtick is not a string delimiter in YAML.
    string: {
      chars: '',
    },

    // Skip number matching when yamlMatcher detected trailing text
    // after a digit-starting value (e.g. "64 characters, hexadecimal.").
    number: {
      check: (_lex: any) => {
        if (skipNumberMatch) {
          skipNumberMatch = false
          return { done: true }
        }
      },
    },

    // Custom text check: consume to end of line (including spaces)
    // for YAML plain scalar values.
    text: {
      check: (lex: any) => {
        let pnt = lex.pnt
        let fwd = lex.fwd

        let ch = fwd[0]
        // Block scalar: | or > (with optional chomping indicator)
        if (ch === '|' || ch === '>') {
          let fold = ch === '>'
          let chomp = 'clip' // default: single trailing newline
          let explicitIndent = 0
          let idx = 1
          // Parse optional chomping and indentation indicators in either order.
          // Valid: |, |+, |-, |2, |+2, |-2, |2+, |2-
          for (let pi = 0; pi < 2; pi++) {
            if (fwd[idx] === '+') { chomp = 'keep'; idx++ }
            else if (fwd[idx] === '-') { chomp = 'strip'; idx++ }
            else if (fwd[idx] >= '1' && fwd[idx] <= '9') { explicitIndent = parseInt(fwd[idx]); idx++ }
          }

          // Must be followed by newline (possibly with trailing spaces/comment)
          while (fwd[idx] === ' ') idx++
          if (fwd[idx] === '#') {
            while (idx < fwd.length && fwd[idx] !== '\n' && fwd[idx] !== '\r') idx++
          }
          if (fwd[idx] !== '\n' && fwd[idx] !== '\r' && fwd[idx] !== undefined) {
            // Not a block scalar — fall through to normal text handling.
          } else {
            // Skip the indicator line.
            if (fwd[idx] === '\r') idx++
            if (fwd[idx] === '\n') idx++

            // Determine block indent from first content line,
            // or use explicit indent indicator if provided.
            let blockIndent = 0
            if (explicitIndent === 0) {
              // Auto-detect: skip blank lines, find first content line.
              let tempIdx = idx
              while (tempIdx < fwd.length) {
                let lineSpaces = 0
                while (tempIdx + lineSpaces < fwd.length && fwd[tempIdx + lineSpaces] === ' ') lineSpaces++
                let afterSpaces = tempIdx + lineSpaces
                if (afterSpaces >= fwd.length || fwd[afterSpaces] === '\n' || fwd[afterSpaces] === '\r') {
                  // Blank line — skip it.
                  tempIdx = afterSpaces
                  if (fwd[tempIdx] === '\r') tempIdx++
                  if (fwd[tempIdx] === '\n') tempIdx++
                  continue
                }
                blockIndent = lineSpaces
                break
              }
            }

            // Determine the indent of the line containing the block indicator.
            // If blockIndent <= that indent, the block is empty (content must
            // be more indented than the containing line). Exception: after ---,
            // the containing indent is effectively -1 so blockIndent 0 is valid.
            let containingIndent = 0
            let isDocStart = false
            {
              let li = pnt.sI - 1
              while (li > 0 && lex.src[li - 1] !== '\n' && lex.src[li - 1] !== '\r') li--
              let lineStart = li
              while (li < pnt.sI && lex.src[li] === ' ') { containingIndent++; li++ }
              // Check if this line starts with --- (document start marker).
              if (lex.src[lineStart] === '-' && lex.src[lineStart+1] === '-' && lex.src[lineStart+2] === '-') {
                isDocStart = true
              }
            }
            // Apply explicit indent relative to containing indent.
            // Per YAML spec, the content indentation = block scalar's indent
            // level + indicator value. The block scalar's indent level is the
            // indent of the containing block (e.g., the mapping key), which
            // may differ from the line's leading spaces (e.g., after "- ").
            if (explicitIndent > 0) {
              // Find the line containing the block indicator.
              let li = pnt.sI - 1
              while (li > 0 && lex.src[li - 1] !== '\n' && lex.src[li - 1] !== '\r') li--
              // li is now at the start of the line. Find the colon position.
              let keyCol = containingIndent
              // Check if there's a colon on the SAME line as the block indicator.
              let hasColonOnLine = false
              for (let ci = li + containingIndent; ci < pnt.sI; ci++) {
                if (lex.src[ci] === ':' && (lex.src[ci+1] === ' ' || lex.src[ci+1] === '\t')) {
                  hasColonOnLine = true
                  break
                }
              }
              if (hasColonOnLine) {
                // Block indicator on same line as colon (e.g., "key: |2").
                // Check for sequence indicators: each "- " adds to the effective indent.
                let scanI = li + containingIndent
                while (scanI < pnt.sI && lex.src[scanI] === '-' &&
                       (lex.src[scanI+1] === ' ' || lex.src[scanI+1] === '\t')) {
                  keyCol += 2
                  scanI += 2
                  while (scanI < pnt.sI && lex.src[scanI] === ' ') { keyCol++; scanI++ }
                }
                blockIndent = keyCol + explicitIndent
              } else {
                // Block indicator on its own line (e.g., after a tag on
                // a separate line). Look backward to find the parent
                // mapping key's indent by scanning previous lines for
                // the colon that started this value context.
                let parentIndent = 0
                let searchI = li - 1
                while (searchI > 0) {
                  // Find start of previous line.
                  if (lex.src[searchI] === '\n') searchI--
                  if (lex.src[searchI] === '\r') searchI--
                  let prevLineEnd = searchI + 1
                  while (searchI > 0 && lex.src[searchI - 1] !== '\n' && lex.src[searchI - 1] !== '\r') searchI--
                  let prevLineStart = searchI
                  // Check if this line has a colon (mapping key).
                  for (let ci = prevLineStart; ci < prevLineEnd; ci++) {
                    if (lex.src[ci] === ':' && (lex.src[ci+1] === ' ' || lex.src[ci+1] === '\t' ||
                        lex.src[ci+1] === '\n' || lex.src[ci+1] === '\r' || ci+1 >= prevLineEnd)) {
                      // Found the parent key line. Get its indent.
                      parentIndent = 0
                      let pi = prevLineStart
                      while (pi < prevLineEnd && lex.src[pi] === ' ') { parentIndent++; pi++ }
                      break
                    }
                  }
                  break  // Only check the immediately preceding non-blank line.
                }
                blockIndent = parentIndent + explicitIndent
                // Update containingIndent to parent's indent so the
                // "blockIndent <= containingIndent" check below works.
                containingIndent = parentIndent
              }
            }
            if (blockIndent <= containingIndent && !isDocStart && idx < fwd.length) {
              // Content is not indented enough — empty block scalar.
              // For keep chomping, count trailing blank lines.
              let val: string
              if (chomp === 'keep') {
                let blankCount = 0
                let bi = idx
                while (bi < fwd.length) {
                  if (fwd[bi] === '\n') { blankCount++; bi++ }
                  else if (fwd[bi] === '\r') { bi++; if (bi < fwd.length && fwd[bi] === '\n') bi++; blankCount++ }
                  else break
                }
                val = '\n'.repeat(blankCount > 0 ? blankCount : 1)
                idx = bi
              } else {
                val = chomp === 'strip' ? '' : ''
              }
              let src = fwd.substring(0, idx)
              let tkn = lex.token('#TX', val, src, pnt)
              pnt.sI += idx
              pnt.rI += 1
              pnt.cI = 0
              return { done: true, token: tkn }
            }

            // Collect indented lines.
            let lines: string[] = []
            let pos = idx
            let rows = 1 // Already consumed one newline
            let lastNewlinePos = idx // Track position before last consumed newline
            while (pos < fwd.length) {
              // Check indent of current line.
              let lineIndent = 0
              while (pos + lineIndent < fwd.length && fwd[pos + lineIndent] === ' ') lineIndent++

              // Blank line (only whitespace before newline or end).
              let afterSpaces = pos + lineIndent
              if (afterSpaces >= fwd.length || fwd[afterSpaces] === '\n' || fwd[afterSpaces] === '\r') {
                // Preserve spaces beyond block indent on blank lines.
                if (lineIndent > blockIndent) {
                  lines.push(fwd.substring(pos + blockIndent, afterSpaces))
                } else {
                  lines.push('')
                }
                lastNewlinePos = afterSpaces
                pos = afterSpaces
                if (fwd[pos] === '\r') pos++
                if (fwd[pos] === '\n') pos++
                rows++
                continue
              }

              // Less indent means end of block.
              if (lineIndent < blockIndent) break

              // Stop at document markers (--- or ...) at indent 0.
              if (lineIndent === 0 &&
                  ((fwd[pos] === '-' && fwd[pos+1] === '-' && fwd[pos+2] === '-' &&
                    (fwd[pos+3] === '\n' || fwd[pos+3] === '\r' || fwd[pos+3] === ' ' || fwd[pos+3] === undefined)) ||
                   (fwd[pos] === '.' && fwd[pos+1] === '.' && fwd[pos+2] === '.' &&
                    (fwd[pos+3] === '\n' || fwd[pos+3] === '\r' || fwd[pos+3] === ' ' || fwd[pos+3] === undefined)))) break

              // Consume the line content (strip block indent).
              let lineStart = pos + blockIndent
              let lineEnd = lineStart
              while (lineEnd < fwd.length && fwd[lineEnd] !== '\n' && fwd[lineEnd] !== '\r') lineEnd++
              lines.push(fwd.substring(lineStart, lineEnd))
              lastNewlinePos = lineEnd
              pos = lineEnd
              if (fwd[pos] === '\r') pos++
              if (fwd[pos] === '\n') pos++
              rows++
            }

            // Build the scalar value.
            let val: string
            if (fold) {
              // Folded: newlines between "normal" lines become spaces.
              // Empty lines and "more indented" lines are preserved literally.
              let result = ''
              let prevWasNormal = false
              let pendingEmptyCount = 0
              for (let li = 0; li < lines.length; li++) {
                let line = lines[li]
                let isMore = line.length > 0 && (line[0] === ' ' || line[0] === '\t')
                let isEmpty = line === ''

                if (isEmpty) {
                  pendingEmptyCount++
                } else if (isMore) {
                  // Flush pending empty lines, with paragraph-close if needed.
                  if (prevWasNormal && result.length > 0) result += '\n'
                  for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
                  pendingEmptyCount = 0
                  // "More indented" — preserve with newlines around it.
                  if (result.length > 0 && result[result.length - 1] !== '\n') {
                    result += '\n'
                  }
                  result += line + '\n'
                  prevWasNormal = false
                } else {
                  // Normal line.
                  if (pendingEmptyCount > 0) {
                    // Empty lines between content: emit them.
                    // The transition normal→empty→normal needs paragraph-close \n
                    // (which counts as the first empty line).
                    if (prevWasNormal && result.length > 0) {
                      // First empty line is the paragraph break.
                      result += '\n'
                      for (let ei = 1; ei < pendingEmptyCount; ei++) result += '\n'
                    } else {
                      for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
                    }
                    pendingEmptyCount = 0
                  }
                  if (prevWasNormal && result.length > 0 && result[result.length - 1] !== '\n') {
                    // Join with space (folding).
                    result += ' '
                  }
                  result += line
                  prevWasNormal = true
                }
              }
              // Flush trailing empty lines.
              for (let ei = 0; ei < pendingEmptyCount; ei++) result += '\n'
              val = result
            } else {
              // Literal: preserve newlines.
              val = lines.join('\n')
            }

            // Apply chomping.
            if (lines.length === 0) {
              // No content lines at all — result is empty string
              // regardless of chomping.
              val = ''
            } else if (chomp === 'strip') {
              val = val.replace(/\n+$/, '')
            } else if (chomp === 'clip') {
              val = val.replace(/\n+$/, '') + '\n'
            } else {
              // keep: preserve all trailing newlines
              val = val + '\n'
            }

            // If block ended because of less indent (more content follows
            // that isn't a doc marker), don't consume the final newline —
            // leave it for the yamlMatcher to emit #IN so the grammar can
            // continue properly.
            let endPos = pos
            let endRows = rows
            if (pos < fwd.length && pos > lastNewlinePos) {
              // Check if the next content is a doc marker (--- or ...).
              let nextLineIndent = 0
              let ni = pos
              while (ni < fwd.length && fwd[ni] === ' ') { nextLineIndent++; ni++ }
              let isDocMarker = nextLineIndent === 0 &&
                ((fwd[ni] === '-' && fwd[ni+1] === '-' && fwd[ni+2] === '-') ||
                 (fwd[ni] === '.' && fwd[ni+1] === '.' && fwd[ni+2] === '.'))
              if (!isDocMarker) {
                // Regular content follows — back up to the newline position.
                endPos = lastNewlinePos
                endRows = rows - 1
              }
            }
            let src = fwd.substring(0, endPos)
            let tkn = lex.token('#TX', val, src, pnt)
            pnt.sI += endPos
            pnt.rI += endRows
            pnt.cI = 0
            return { done: true, token: tkn }
          }
        }

        // YAML tags: !!type value
        if (ch === '!' && fwd[1] === '!') {
          let tagEnd = 2
          while (tagEnd < fwd.length && fwd[tagEnd] !== ' ' && fwd[tagEnd] !== '\n' &&
                 fwd[tagEnd] !== '\r') tagEnd++
          let tag = fwd.substring(2, tagEnd)
          // For !!seq and !!map, these are handled in the yamlMatcher.
          if (tag === 'seq' || tag === 'map') {
            return null  // Don't advance pnt — let yamlMatcher handle it.
          }
          // For value tags, parse the value after the tag.
          let valStart = tagEnd
          if (fwd[valStart] === ' ') valStart++
          // Get the raw value string.
          let rawVal = ''
          let valEnd = valStart
          if (fwd[valStart] === '"' || fwd[valStart] === "'") {
            // Quoted value — find matching close quote.
            let q = fwd[valStart]
            valEnd = valStart + 1
            while (valEnd < fwd.length && fwd[valEnd] !== q) {
              if (fwd[valEnd] === '\\' && q === '"') valEnd++  // Skip escape in double quotes.
              valEnd++
            }
            if (fwd[valEnd] === q) valEnd++
            rawVal = fwd.substring(valStart + 1, valEnd - 1)
          } else {
            // Unquoted value — to end of line, stopping at `: ` and ` #`.
            while (valEnd < fwd.length && fwd[valEnd] !== '\n' && fwd[valEnd] !== '\r') {
              if (fwd[valEnd] === ':' && (fwd[valEnd+1] === ' ' || fwd[valEnd+1] === '\n' ||
                  fwd[valEnd+1] === '\r' || fwd[valEnd+1] === undefined)) break
              if (fwd[valEnd] === ' ' && fwd[valEnd+1] === '#') break
              valEnd++
            }
            rawVal = fwd.substring(valStart, valEnd).replace(/\s+$/, '')
          }
          // Apply tag conversion.
          let result: any
          if (tag === 'str') result = String(rawVal)
          else if (tag === 'int') result = parseInt(rawVal, 10)
          else if (tag === 'float') result = parseFloat(rawVal)
          else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
          else if (tag === 'null') result = null
          else result = rawVal  // Unknown tag — keep as string.

          let src = fwd.substring(0, valEnd)
          let tknTin = typeof result === 'string' ? '#TX' :
                       typeof result === 'number' ? '#NR' :
                       '#VL'
          let tkn = lex.token(tknTin, result, src, pnt)
          pnt.sI += valEnd
          pnt.cI += valEnd
          return { done: true, token: tkn }
        }

        // Don't apply text check for special chars or flow context.
        // Also skip * and & which are YAML alias/anchor indicators.
        if (ch === '{' || ch === '}' || ch === '[' || ch === ']' ||
            ch === ',' || ch === '#' || ch === '\n' ||
            ch === '\r' || ch === '"' || ch === "'" ||
            ch === '*' || ch === '&' || ch === '!' ||
            ch === undefined) {
          return null
        }
        // Colon only starts a key-value separator if followed by space/tab/newline/eof.
        // Otherwise it can start a plain scalar (e.g. ::vector).
        if (ch === ':' && (fwd[1] === ' ' || fwd[1] === '\t' || fwd[1] === '\n' ||
            fwd[1] === '\r' || fwd[1] === undefined)) {
          return null
        }

        // Match text to end of line, stopping at `: `, `:\n`, ` #`, or newline.
        // This handles YAML plain scalars with multiline continuation.
        updateFlowState(lex.src as string, pnt.sI)
        let inFlowCtx = _flowDepth > 0
        // Find key indent and determine context for multiline scalars.
        let lineStart = pnt.sI
        while (lineStart > 0 && lex.src[lineStart - 1] !== '\n' && lex.src[lineStart - 1] !== '\r') lineStart--

        // Current line indent (indent of the line where text starts).
        let currentLineIndent = 0
        {
          let ci = lineStart
          while (ci < pnt.sI && lex.src[ci] === ' ') { currentLineIndent++; ci++ }
        }

        // Check if text is preceded by ": " on the same line (map value context).
        let isMapValue = false
        {
          let ci = pnt.sI - 1
          // Skip whitespace before text
          while (ci >= lineStart && (lex.src[ci] === ' ' || lex.src[ci] === '\t')) ci--
          if (ci >= lineStart && lex.src[ci] === ':') isMapValue = true
        }

        // For map values: continuation requires indent > key indent (parent line's indent).
        // For standalone scalars: continuation requires indent >= current line indent.
        let keyIndent = 0
        let prevLineStart = lineStart
        if (prevLineStart > 0) {
          let pi = prevLineStart - 1
          if (pi >= 0 && lex.src[pi] === '\n') pi--
          if (pi >= 0 && lex.src[pi] === '\r') pi--
          while (pi > 0 && lex.src[pi - 1] !== '\n' && lex.src[pi - 1] !== '\r') pi--
          while (pi < prevLineStart && lex.src[pi] === ' ') { keyIndent++; pi++ }
        }

        // The minimum indent for continuation lines.
        // For map values, continuation indent is based on the colon's line indent,
        // not the previous line's indent (which may be a key continuation line).
        let minContinuationIndent = isMapValue ? currentLineIndent + 1 : currentLineIndent
        let text = ''
        let i = 0
        let totalConsumed = 0
        let rows = 0
        let scanLine = () => {
          let line = ''
          while (i < fwd.length) {
            let c = fwd[i]
            if (c === '\n' || c === '\r') break
            if (c === ':' && (fwd[i + 1] === ' ' || fwd[i + 1] === '\t' || fwd[i + 1] === '\n' ||
                fwd[i + 1] === '\r' || fwd[i + 1] === undefined)) break
            if ((c === ' ' || c === '\t') && fwd[i + 1] === '#') break
            if (inFlowCtx && (c === ']' || c === '}')) break
            if (c === ',' && inFlowCtx) break
            line += c
            i++
          }
          return line.replace(/\s+$/, '')
        }

        text = scanLine()
        totalConsumed = i

        // Check for continuation lines (multiline plain scalars).
        // Blank lines (whitespace-only) within a scalar become newlines.
        while (i < fwd.length && (fwd[i] === '\n' || fwd[i] === '\r')) {
          let nlPos = i
          // Count blank lines (lines with only whitespace).
          let blankLines = 0
          while (i < fwd.length && (fwd[i] === '\n' || fwd[i] === '\r')) {
            if (fwd[i] === '\r') i++
            if (fwd[i] === '\n') i++
            // Count indent of next line.
            let li = 0
            while (i + li < fwd.length && (fwd[i + li] === ' ' || fwd[i + li] === '\t')) li++
            if (i + li >= fwd.length || fwd[i + li] === '\n' || fwd[i + li] === '\r') {
              // Blank line — count it and skip.
              blankLines++
              i += li
              continue
            }
            break
          }
          // Count indent of the content line after blank lines.
          let lineIndent = 0
          while (i < fwd.length && (fwd[i] === ' ' || fwd[i] === '\t')) { lineIndent++; i++ }
          // In flow context, continuation is allowed regardless of indent
          // (as long as the next line doesn't start a flow indicator or comment).
          // In block context, must be more indented than the key.
          // Check for document markers (--- or ...) at column 0.
          let isDocMarker = lineIndent === 0 &&
            ((fwd[i] === '-' && fwd[i+1] === '-' && fwd[i+2] === '-' &&
              (fwd[i+3] === ' ' || fwd[i+3] === '\t' || fwd[i+3] === '\n' ||
               fwd[i+3] === '\r' || fwd[i+3] === undefined)) ||
             (fwd[i] === '.' && fwd[i+1] === '.' && fwd[i+2] === '.' &&
              (fwd[i+3] === ' ' || fwd[i+3] === '\t' || fwd[i+3] === '\n' ||
               fwd[i+3] === '\r' || fwd[i+3] === undefined)))
          // Check for sequence marker "- ". Only treat as a new sequence
          // entry when the indent matches an enclosing sequence's level.
          // Find the nearest "- " sequence marker preceding the text on
          // the first line to determine the relevant sequence indent.
          let isSeqMarker = false
          if (fwd[i] === '-' &&
              (fwd[i+1] === ' ' || fwd[i+1] === '\t' || fwd[i+1] === '\n' ||
               fwd[i+1] === '\r' || fwd[i+1] === undefined)) {
            // Determine the sequence indent from the first line's context.
            // Look backward from pnt.sI to find "- " markers before the text.
            let seqIndent = -1
            let si = pnt.sI - 1
            while (si >= lineStart) {
              if (lex.src[si] === '-' && (lex.src[si+1] === ' ' || lex.src[si+1] === '\t')) {
                seqIndent = si - lineStart
                break
              }
              si--
            }
            // isSeqMarker if the continuation "- " matches a known sequence
            // indent, or if it's at the current line indent level.
            isSeqMarker = (seqIndent >= 0 && lineIndent === seqIndent) ||
                          (seqIndent < 0 && lineIndent <= currentLineIndent)
          }
          let canContinue = inFlowCtx
            ? (i < fwd.length && fwd[i] !== '\n' && fwd[i] !== '\r' &&
               fwd[i] !== '#' && fwd[i] !== '{' && fwd[i] !== '}' &&
               fwd[i] !== '[' && fwd[i] !== ']')
            : (lineIndent >= minContinuationIndent && i < fwd.length &&
               fwd[i] !== '\n' && fwd[i] !== '\r' && fwd[i] !== '#' &&
               !isDocMarker && !isSeqMarker)
          if (canContinue) {
            // Check if this line is a key-value pair (contains ": ").
            let peekJ = i
            let isKV = false
            while (peekJ < fwd.length && fwd[peekJ] !== '\n' && fwd[peekJ] !== '\r') {
              if (fwd[peekJ] === ':' && (fwd[peekJ + 1] === ' ' || fwd[peekJ + 1] === '\t' ||
                  fwd[peekJ + 1] === '\n' || fwd[peekJ + 1] === '\r' ||
                  fwd[peekJ + 1] === undefined)) {
                isKV = true
                break
              }
              if (fwd[peekJ] === '}' || fwd[peekJ] === ']' || fwd[peekJ] === ',') {
                break
              }
              peekJ++
            }
            if (!isKV || inFlowCtx) {
              let contLine = scanLine()
              if (contLine.length > 0) {
                // Blank lines → newlines; single newline → space (folding).
                if (blankLines > 0) {
                  for (let b = 0; b < blankLines; b++) text += '\n'
                } else {
                  text += ' '
                }
                text += contLine
                totalConsumed = i
                rows++
                continue
              }
            }
          }
          // Not a continuation — revert to newline position.
          i = nlPos
          break
        }

        text = text.replace(/\s+$/, '')

        if (text.length === 0) return null

        // Check if this is a known YAML value.
        let valMap: Record<string, any> = {
          'true': true, 'True': true, 'TRUE': true,
          'false': false, 'False': false, 'FALSE': false,
          'null': null, 'Null': null, 'NULL': null,
          '~': null,
          'yes': true, 'Yes': true, 'YES': true,
          'no': false, 'No': false, 'NO': false,
          'on': true, 'On': true, 'ON': true,
          'off': false, 'Off': false, 'OFF': false,
          '.inf': Infinity, '.Inf': Infinity, '.INF': Infinity,
          '-.inf': -Infinity, '-.Inf': -Infinity, '-.INF': -Infinity,
          '.nan': NaN, '.NaN': NaN, '.NAN': NaN,
        }
        if (text in valMap) {
          let tkn = lex.token('#VL', valMap[text], text, pnt)
          pnt.sI += text.length
          pnt.cI += text.length
          return { done: true, token: tkn }
        }

        // Check if it's a number.
        let num = +text
        if (!isNaN(num) && text !== '') {
          let tkn = lex.token('#NR', num, text, pnt)
          pnt.sI += text.length
          pnt.cI += text.length
          return { done: true, token: tkn }
        }

        // Plain text — consume to end of meaningful content.
        let src = fwd.substring(0, totalConsumed)
        let tkn = lex.token('#TX', text, src, pnt)
        pnt.sI += totalConsumed
        pnt.rI += rows
        pnt.cI += totalConsumed  // approximate
        return { done: true, token: tkn }
      },
    },
  })

  // Register #EL token (not as a fixed token — we match it in yamlMatcher).
  let EL = jsonic.token('#EL')

  // Register #QM token: the YAML `?` explicit-key indicator inside flow
  // collections. Emitted by yamlMatcher only when in flow context and
  // followed by whitespace; consumed by pair/elem rule alts below.
  let QM = jsonic.token('#QM')

  // YAML document-frame tokens, emitted by yamlMatcher at column 0:
  //   #DS  document start: ---     (with optional inline content following)
  //   #DE  document end:   ...
  //   #DR  directive line: %YAML 1.2  /  %TAG !! tag:...
  // The `stream` rule consumes them; rules apply directives and accumulate
  // each document's value into the result.
  let DS = jsonic.token('#DS')
  let DE = jsonic.token('#DE')
  let DR = jsonic.token('#DR')

  // Flow collection tokens.
  let CA = jsonic.token.CA  // comma
  let CS = jsonic.token.CS  // ]
  let CB = jsonic.token.CB  // }
  let OS = jsonic.token.OS  // [
  let OB = jsonic.token.OB  // {

  // All tokens that can start a value.
  let KEY = [TX, NR, ST, VL]

  // Add a custom lex matcher for YAML special cases.
  jsonic.options({
    lex: {
      match: {
        yaml: {
          order: 5e5,
          make: (_cfg: Config, _opts: Options) => {
            // Track Lex objects we've already initialised. Identity comparison
            // distinguishes parse invocations correctly even when the same
            // source string is parsed twice in a row — `lex.src !== cleanedSrc`
            // would skip the reset on the second call and pollute output with
            // state from the prior parse.
            const seenLex: WeakSet<Lex> = new WeakSet()
            return function yamlMatcher(lex: Lex) {
              // First call of a new parse: reset per-parse state.
              // Document-frame syntax (--- / ... / %YAML / %TAG) is no longer
              // mutated out of lex.src here — it flows through as #DS / #DE /
              // #DR tokens consumed by the `stream` rule.
              if (!seenLex.has(lex)) {
                seenLex.add(lex)
                anchors = {}
                pendingAnchors = []
                pendingExplicitCL = false
                skipNumberMatch = false
                pendingTokens = []
                tagHandles = {}
                yamlStreamDocs = []
                yamlStreamMeta = []
                yamlStreamCurMeta = null
                _flowDepth = 0
                _flowScanPos = 0
                _inSingleQuote = false
                _inDoubleQuote = false
                // Empty / whitespace-only / comments-only source: emit one
                // null #VL so the parser yields `null` rather than an error.
                let src = '' + lex.src
                let stripped = src.replace(/^[ \t]*#[^\n]*(\n|$)/gm, '').trim()
                if (src.trim() === '' || stripped === '') {
                  lex.pnt.len = 0
                  let tkn = lex.token('#VL', null, '', lex.pnt)
                  lex.pnt.sI = 0
                  return tkn
                }
              }
              // Drain any queued tokens first (from multi-token explicit keys).
              if (pendingTokens.length > 0) {
                return pendingTokens.shift()
              }

              let pnt = lex.pnt
              let fwd = lex.fwd

              // Loop to restart matching after consuming flow whitespace.
              yamlMatchLoop: while (true) {

              // Skip blank lines that contain only tabs (and maybe spaces).
              // YAML treats these as blank lines, but jsonic errors on bare tabs.
              if (fwd[0] === '\t' || fwd[0] === ' ') {
                let lineEnd = fwd.indexOf('\n')
                let lineContent = lineEnd >= 0 ? fwd.substring(0, lineEnd) : fwd
                if (lineContent.indexOf('\t') >= 0 && /^[ \t]+$/.test(lineContent)) {
                  let skip = lineEnd >= 0 ? lineEnd + 1 : lineContent.length
                  pnt.sI += skip
                  pnt.rI++
                  pnt.cI = 0
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }
              }

              // Emit pending CL from explicit key — must be before !!type handlers
              // so the CL token appears before the value token.
              if (pendingExplicitCL) {
                pendingExplicitCL = false
                let tkn = lex.token('#CL', 1, ': ', lex.pnt)
                return tkn
              }

              // YAML alias: *name — emit a VL token with alias name.
              // Resolution happens at grammar time (val.ac) since the anchor
              // may not be recorded yet due to lexer pre-fetching.
              if (fwd[0] === '*') {
                let nameEnd = 1
                while (nameEnd < fwd.length && fwd[nameEnd] !== ' ' && fwd[nameEnd] !== '\t' &&
                       fwd[nameEnd] !== '\n' && fwd[nameEnd] !== '\r' && fwd[nameEnd] !== ',' &&
                       fwd[nameEnd] !== '{' && fwd[nameEnd] !== '}' && fwd[nameEnd] !== '[' &&
                       fwd[nameEnd] !== ']') {
                  // Colon terminates only when followed by space/tab (key-value separator).
                  // Otherwise colon is a valid anchor-name character per YAML spec.
                  if (fwd[nameEnd] === ':' &&
                      (fwd[nameEnd+1] === ' ' || fwd[nameEnd+1] === '\t')) break
                  nameEnd++
                }
                let name = fwd.substring(1, nameEnd)
                let src = fwd.substring(0, nameEnd)
                // Check if this alias is used as a map key (followed by ` :` or `:`).
                let afterAlias = nameEnd
                while (afterAlias < fwd.length && (fwd[afterAlias] === ' ' || fwd[afterAlias] === '\t')) afterAlias++
                let isKey = afterAlias < fwd.length && fwd[afterAlias] === ':' &&
                  (fwd[afterAlias+1] === ' ' || fwd[afterAlias+1] === '\t' ||
                   fwd[afterAlias+1] === '\n' || fwd[afterAlias+1] === '\r' ||
                   fwd[afterAlias+1] === undefined)
                if (isKey && anchors[name] !== undefined) {
                  // Resolve alias immediately as a key string.
                  let resolved = String(anchors[name])
                  let tkn = lex.token('#TX', resolved, src, lex.pnt)
                  pnt.sI += nameEnd
                  pnt.cI += nameEnd
                  return tkn
                }
                // Resolve alias immediately if anchor exists, since deferred
                // markers can be lost through Jsonic's rule processing.
                let tkn: any
                if (anchors[name] !== undefined) {
                  let val = anchors[name]
                  if (typeof val === 'object' && val !== null) {
                    val = JSON.parse(JSON.stringify(val))
                  }
                  let tin = typeof val === 'string' ? '#TX' :
                            typeof val === 'number' ? '#NR' : '#VL'
                  tkn = lex.token(tin, val, src, lex.pnt)
                } else {
                  // Anchor not yet seen — store marker for deferred resolution.
                  let marker = { __yamlAlias: name }
                  tkn = lex.token('#VL', marker, src, lex.pnt)
                }
                pnt.sI += nameEnd
                pnt.cI += nameEnd
                return tkn
              }

              // YAML anchor: &name — store value. Skip the anchor marker,
              // let the value be parsed, and record it post-parse via grammar rules.
              if (fwd[0] === '&') {
                let nameEnd = 1
                while (nameEnd < fwd.length && fwd[nameEnd] !== ' ' && fwd[nameEnd] !== '\t' &&
                       fwd[nameEnd] !== '\n' && fwd[nameEnd] !== '\r' && fwd[nameEnd] !== ',' &&
                       fwd[nameEnd] !== '{' && fwd[nameEnd] !== '}' && fwd[nameEnd] !== '[' &&
                       fwd[nameEnd] !== ']') nameEnd++
                let anchorName = fwd.substring(1, nameEnd)
                let skip = nameEnd
                if (fwd[skip] === ' ' || fwd[skip] === '\t') skip++
                // Check if anchor is standalone (first content on its line).
                // Look backward to see if only whitespace precedes & on this line.
                let isStandalone = true
                let anchorIndent = 0
                {
                  let bi = pnt.sI - 1
                  while (bi >= 0 && lex.src[bi] !== '\n' && lex.src[bi] !== '\r') {
                    if (lex.src[bi] !== ' ' && lex.src[bi] !== '\t') {
                      isStandalone = false
                      break
                    }
                    anchorIndent++
                    bi--
                  }
                }
                pnt.sI += skip
                pnt.cI += skip
                // Determine if anchor is inline (content follows on same line)
                // or standalone (only newline follows).
                let anchorInline = !(isStandalone &&
                    (lex.src[pnt.sI] === '\r' || lex.src[pnt.sI] === '\n' ||
                     pnt.sI >= lex.src.length))
                // For inline anchors before scalar values, record the anchor
                // immediately so aliases in later pairs can resolve them.
                if (anchorInline) {
                  let peek = lex.refwd()
                  let pch = peek[0]
                  if (pch !== '[' && pch !== '{' && pch !== '>' && pch !== '|' &&
                      pch !== '\n' && pch !== '\r' && pch !== undefined) {
                    let scalarVal: string | undefined
                    if (pch === '"') {
                      let ei = 1
                      while (ei < peek.length && peek[ei] !== '"') {
                        if (peek[ei] === '\\') ei++
                        ei++
                      }
                      scalarVal = peek.substring(1, ei)
                        .replace(/\\n/g, '\n').replace(/\\t/g, '\t')
                        .replace(/\\\\/g, '\\').replace(/\\"/g, '"')
                    } else if (pch === "'") {
                      let ei = 1
                      while (ei < peek.length && peek[ei] !== "'") {
                        if (peek[ei] === "'" && peek[ei+1] === "'") ei++
                        ei++
                      }
                      scalarVal = peek.substring(1, ei).replace(/''/g, "'")
                    } else {
                      let ei = 0
                      while (ei < peek.length && peek[ei] !== '\n' && peek[ei] !== '\r' &&
                             peek[ei] !== ',' && peek[ei] !== '}' && peek[ei] !== ']') {
                        if (peek[ei] === ':' && (peek[ei+1] === ' ' || peek[ei+1] === '\t' ||
                            peek[ei+1] === '\n' || peek[ei+1] === '\r' ||
                            peek[ei+1] === undefined)) break
                        if (peek[ei] === ' ' && peek[ei+1] === '#') break
                        ei++
                      }
                      let raw = peek.substring(0, ei).trim()
                      if (raw.length > 0) scalarVal = raw
                    }
                    if (scalarVal !== undefined) {
                      anchors[anchorName] = scalarVal
                    }
                  }
                }
                // Push pending anchor with inline flag.
                pendingAnchors.push({ name: anchorName, inline: anchorInline })
                // If anchor is standalone on its own line (followed by newline),
                // consume the newline and leading spaces so no extra IN token
                // is emitted. Only consume when next line indent >= anchor indent,
                // otherwise let the normal indent handler manage the transition.
                if (isStandalone &&
                    (lex.src[pnt.sI] === '\r' || lex.src[pnt.sI] === '\n')) {
                  let nl = pnt.sI
                  if (lex.src[nl] === '\r') nl++
                  if (lex.src[nl] === '\n') nl++
                  let spaces = 0
                  while (nl + spaces < lex.src.length && lex.src[nl + spaces] === ' ') spaces++
                  let nextCh = lex.src[nl + spaces]
                  if (nextCh !== undefined && nextCh !== '\n' && nextCh !== '\r' &&
                      spaces >= anchorIndent) {
                    pnt.sI = nl + spaces
                    pnt.cI = spaces
                    pnt.rI++
                  }
                }
                // Update fwd and continue matching.
                fwd = lex.refwd()
                continue yamlMatchLoop
              }

              // YAML directive line (%YAML, %TAG, %FOO) at column 0: emit a
              // #DR token whose val is the raw directive text. The stream rule
              // applies the directive (e.g. %TAG handle registration) at parse
              // time via the @apply-directive action.
              if ((pnt.sI === 0 || lex.src[pnt.sI - 1] === '\n' ||
                   lex.src[pnt.sI - 1] === '\r') && fwd[0] === '%') {
                let pos = 0
                while (pos < fwd.length && fwd[pos] !== '\n' && fwd[pos] !== '\r') pos++
                let directiveSrc = fwd.substring(0, pos)
                pnt.sI += pos
                pnt.cI += pos
                let tkn = lex.token('#DR', directiveSrc, directiveSrc, lex.pnt)
                return tkn
              }

              // YAML non-specific tag (! value) or local tag (!name value):
              // skip the tag and let the value be parsed normally.
              if (fwd[0] === '!' && fwd[1] !== '!' && fwd[1] !== undefined) {
                if (fwd[1] === ' ') {
                  // Non-specific tag: ! value → treat value as string.
                  let valStart = 2
                  let valEnd = valStart
                  while (valEnd < fwd.length && fwd[valEnd] !== '\n' && fwd[valEnd] !== '\r') valEnd++
                  let rawVal = fwd.substring(valStart, valEnd).replace(/\s+$/, '')
                  let src = fwd.substring(0, valEnd)
                  let tkn = lex.token('#TX', rawVal, src, lex.pnt)
                  pnt.sI += valEnd
                  pnt.cI += valEnd
                  return tkn
                }
                // Local tag: !name value → skip the tag, continue with value.
                let tagEnd = 1
                while (tagEnd < fwd.length && fwd[tagEnd] !== ' ' && fwd[tagEnd] !== '\n' &&
                       fwd[tagEnd] !== '\r') tagEnd++
                if (fwd[tagEnd] === ' ') tagEnd++ // skip space after tag
                pnt.sI += tagEnd
                pnt.cI += tagEnd
                // If tag is standalone (followed by newline), consume the
                // newline and leading spaces so no extra #IN is emitted.
                if (pnt.sI < lex.src.length &&
                    (lex.src[pnt.sI] === '\n' || lex.src[pnt.sI] === '\r')) {
                  // Check if tag is standalone on its line.
                  let tagStandalone = true
                  let tagLineIndent = 0
                  let bi = pnt.sI - tagEnd - 1
                  while (bi >= 0 && lex.src[bi] !== '\n' && lex.src[bi] !== '\r') {
                    if (lex.src[bi] !== ' ' && lex.src[bi] !== '\t') {
                      tagStandalone = false
                      break
                    }
                    tagLineIndent++
                    bi--
                  }
                  if (tagStandalone) {
                    let nl = pnt.sI
                    if (lex.src[nl] === '\r') nl++
                    if (lex.src[nl] === '\n') nl++
                    let spaces = 0
                    while (nl + spaces < lex.src.length && lex.src[nl + spaces] === ' ') spaces++
                    pnt.sI = nl + spaces
                    pnt.cI = spaces
                    pnt.rI++
                  }
                }
                fwd = lex.refwd()
                // Restart matching to parse the value.
                continue yamlMatchLoop
              }

              // Skip !!seq, !!map, !!omap, !!set, !!binary, etc. tags — just
              // consume and return undefined so the next lex cycle handles the
              // actual structure/value.
              if (fwd[0] === '!' && fwd[1] === '!' &&
                  /^!!(seq|map|omap|set|pairs|binary|ordered|python\/[^\s]*)\b/.test(fwd)) {
                let skip = 2
                while (skip < fwd.length && fwd[skip] !== ' ' && fwd[skip] !== '\n') skip++
                while (skip < fwd.length && fwd[skip] === ' ') skip++
                // Check if tag is standalone on its own line.
                let tagIndent = 0
                {
                  let bi = pnt.sI - 1
                  let standalone = true
                  while (bi >= 0 && lex.src[bi] !== '\n' && lex.src[bi] !== '\r') {
                    if (lex.src[bi] !== ' ' && lex.src[bi] !== '\t') {
                      standalone = false
                      break
                    }
                    tagIndent++
                    bi--
                  }
                  // If standalone and next line is at the same indent, consume
                  // the newline so no extra IN token is emitted.
                  if (standalone && skip < fwd.length &&
                      (fwd[skip] === '\n' || fwd[skip] === '\r')) {
                    let nl = skip
                    if (fwd[nl] === '\r') nl++
                    if (fwd[nl] === '\n') nl++
                    let spaces = 0
                    while (nl + spaces < fwd.length && fwd[nl + spaces] === ' ') spaces++
                    if (spaces >= tagIndent) {
                      skip = nl + spaces
                      pnt.sI += skip
                      pnt.cI = spaces
                      pnt.rI++
                      fwd = lex.refwd()
                      continue yamlMatchLoop
                    }
                  }
                }
                pnt.sI += skip
                pnt.cI += skip
                // Don't return a token — let the next lex cycle see the actual value.
                fwd = lex.refwd()
                continue yamlMatchLoop
              }

              // Handle other !!type tags (!!str, !!int, !!float, !!bool, !!null).
              // These apply a type to the following value. For !!str, the value
              // is always a string. For others, convert accordingly.
              if (fwd[0] === '!' && fwd[1] === '!') {
                let tagEnd = 2
                while (tagEnd < fwd.length && fwd[tagEnd] !== ' ' && fwd[tagEnd] !== '\n' &&
                       fwd[tagEnd] !== '\r' && fwd[tagEnd] !== ',' &&
                       fwd[tagEnd] !== '}' && fwd[tagEnd] !== ']' &&
                       fwd[tagEnd] !== ':') tagEnd++
                let tag = fwd.substring(2, tagEnd)
                let valStart = tagEnd
                if (fwd[valStart] === ' ') valStart++
                let valEnd = valStart
                // Skip and record anchor (&name) if present before value.
                let tagAnchorName = ''
                if (fwd[valStart] === '&') {
                  let anchorEnd = valStart + 1
                  while (anchorEnd < fwd.length && fwd[anchorEnd] !== ' ' &&
                         fwd[anchorEnd] !== '\n' && fwd[anchorEnd] !== '\r') anchorEnd++
                  tagAnchorName = fwd.substring(valStart + 1, anchorEnd)
                  pendingAnchors.push({ name: tagAnchorName, inline: true })
                  if (fwd[anchorEnd] === ' ') anchorEnd++
                  valStart = anchorEnd
                  valEnd = valStart
                }
                // Check for quoted value.
                if (fwd[valStart] === '"' || fwd[valStart] === "'") {
                  let q = fwd[valStart]
                  valEnd = valStart + 1
                  while (valEnd < fwd.length && fwd[valEnd] !== q) {
                    if (fwd[valEnd] === '\\' && q === '"') valEnd++
                    valEnd++
                  }
                  if (fwd[valEnd] === q) valEnd++
                  let rawVal = fwd.substring(valStart + 1, valEnd - 1)
                  let result: any = rawVal
                  if (!tagHandles['!!']) {
                    if (tag === 'int') result = parseInt(rawVal, 10)
                    else if (tag === 'float') result = parseFloat(rawVal)
                    else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
                    else if (tag === 'null') result = null
                  }
                  if (tagAnchorName) anchors[tagAnchorName] = result
                  let tknTin = typeof result === 'string' ? '#TX' :
                               typeof result === 'number' ? '#NR' : '#VL'
                  let tkn = lex.token(tknTin, result, fwd.substring(0, valEnd), lex.pnt)
                  pnt.sI += valEnd
                  pnt.cI += valEnd
                  return tkn
                }
                // If value is on next line (tag followed by newline with
                // indented content), skip the tag and let the next lex cycle
                // handle the value. If end-of-source or next line is not
                // indented content, fall through to produce default value.
                if ((fwd[valStart] === '\n' || fwd[valStart] === '\r') &&
                    valStart < fwd.length - 1) {
                  // Tag followed by newline — skip the tag and let the
                  // next lex cycle handle the value on the following line.
                  let nl = valStart
                  if (fwd[nl] === '\r') nl++
                  if (fwd[nl] === '\n') nl++
                  pnt.sI += nl
                  pnt.cI = 0
                  pnt.rI++
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }
                // Unquoted: stop at `: `, ` #`, newline, flow indicators.
                while (valEnd < fwd.length && fwd[valEnd] !== '\n' && fwd[valEnd] !== '\r' &&
                       fwd[valEnd] !== ',' && fwd[valEnd] !== '}' && fwd[valEnd] !== ']') {
                  if (fwd[valEnd] === ':' && (fwd[valEnd+1] === ' ' || fwd[valEnd+1] === '\n' ||
                      fwd[valEnd+1] === '\r' || fwd[valEnd+1] === undefined)) break
                  if (fwd[valEnd] === ' ' && fwd[valEnd+1] === '#') break
                  valEnd++
                }
                let rawVal = fwd.substring(valStart, valEnd).replace(/\s+$/, '')
                let result: any = rawVal
                // Only apply built-in type conversion when !! has not been
                // redefined by a %TAG directive. Custom tag handles mean
                // !!type is a user-defined tag, not a YAML core type.
                if (!tagHandles['!!']) {
                  if (tag === 'str') result = String(rawVal)
                  else if (tag === 'int') result = parseInt(rawVal, 10)
                  else if (tag === 'float') result = parseFloat(rawVal)
                  else if (tag === 'bool') result = rawVal === 'true' || rawVal === 'True' || rawVal === 'TRUE'
                  else if (tag === 'null') result = null
                }
                if (tagAnchorName) anchors[tagAnchorName] = result
                // Use #ST for empty strings (jsonic handles #ST better than
                // empty #TX in flow context), #NR for numbers, #VL for null.
                let tknTin = (typeof result === 'string' && result === '') ? '#ST' :
                             typeof result === 'string' ? '#TX' :
                             typeof result === 'number' ? '#NR' : '#VL'
                let tkn = lex.token(tknTin, result, fwd.substring(0, valEnd), lex.pnt)
                pnt.sI += valEnd
                pnt.cI += valEnd
                return tkn
              }

              // Flow-context `?` explicit-key marker: emit a #QM token so
              // pair/elem rule alts can handle it. Block-context `?` falls
              // through to the heavyweight handler below.
              if (fwd[0] === '?' && (fwd[1] === ' ' || fwd[1] === '\t')) {
                updateFlowState(lex.src as string, pnt.sI)
                if (_flowDepth > 0) {
                  let tkn = lex.token('#QM', undefined, '?', lex.pnt)
                  pnt.sI += 1; pnt.cI += 1
                  return tkn
                }
              }

              // YAML explicit key indicator: ? key\n: value
              // Handles: ? key (with null value if no : follows)
              //          ? key\n: value
              //          ? key\n# comment\n: value
              //          ? key1\n? key2 (consecutive explicit keys with null values)
              if (fwd[0] === '?' && (fwd[1] === ' ' || fwd[1] === '\t' ||
                  fwd[1] === '\n' || fwd[1] === '\r' || fwd[1] === undefined)) {
                let start = (fwd[1] === ' ' || fwd[1] === '\t') ? 2 : 1
                // Collect key text (may be multiline via continuation).
                let keyEnd = start
                let key = ''
                // First line of key.
                while (keyEnd < fwd.length && fwd[keyEnd] !== '\n' && fwd[keyEnd] !== '\r') {
                  if (fwd[keyEnd] === ' ' && fwd[keyEnd+1] === '#') break  // comment
                  keyEnd++
                }
                key = fwd.substring(start, keyEnd).replace(/\s+$/, '')
                // Strip !!type tags from explicit keys and apply conversion.
                let explicitKeyTag = ''
                let tagMatch = key.match(/^!!(\w+)\s+(.*)$/)
                if (tagMatch) {
                  explicitKeyTag = tagMatch[1]
                  key = tagMatch[2]
                }
                let consumed = keyEnd
                // Track position before consuming newline (for !hasValue case).
                let beforeNewline = consumed
                // Skip comment at end of key line.
                while (consumed < fwd.length && fwd[consumed] !== '\n' && fwd[consumed] !== '\r') consumed++
                beforeNewline = consumed
                // Consume newline after key line.
                if (consumed < fwd.length && fwd[consumed] === '\r') consumed++
                if (consumed < fwd.length && fwd[consumed] === '\n') consumed++
                // Check for multiline key (continuation lines indented more than ?).
                let qIndent = 0
                {
                  let li = pnt.sI
                  while (li > 0 && lex.src[li-1] !== '\n' && lex.src[li-1] !== '\r') li--
                  while (li < pnt.sI && lex.src[li] === ' ') { qIndent++; li++ }
                }
                // Count extra rows consumed (for multiline keys).
                let extraRows = 0

                // Handle block scalar keys (| or >).
                let blockScalarMatch = key.match(/^([|>])([+-]?)([0-9]?)$/)
                if (blockScalarMatch) {
                  let isFolded = blockScalarMatch[1] === '>'
                  let chomp = blockScalarMatch[2] || ''
                  let explicitIndent = blockScalarMatch[3] ? parseInt(blockScalarMatch[3]) : 0
                  // Collect block scalar content lines.
                  let blockLines: string[] = []
                  let contentIndent = 0
                  while (consumed < fwd.length) {
                    let lineIndent = 0
                    while (consumed + lineIndent < fwd.length && fwd[consumed + lineIndent] === ' ') lineIndent++
                    let afterSpaces = consumed + lineIndent
                    // Empty line or line with only spaces.
                    if (afterSpaces >= fwd.length || fwd[afterSpaces] === '\n' || fwd[afterSpaces] === '\r') {
                      blockLines.push('')
                      consumed = afterSpaces
                      if (consumed < fwd.length && fwd[consumed] === '\r') consumed++
                      if (consumed < fwd.length && fwd[consumed] === '\n') consumed++
                      extraRows++
                      continue
                    }
                    // Determine content indent from first non-empty line.
                    if (contentIndent === 0) {
                      contentIndent = explicitIndent > 0 ? qIndent + explicitIndent : lineIndent
                    }
                    // Line must be indented more than ? to be content.
                    if (lineIndent < contentIndent) break
                    // Collect line content.
                    let lineEnd = afterSpaces
                    while (lineEnd < fwd.length && fwd[lineEnd] !== '\n' && fwd[lineEnd] !== '\r') lineEnd++
                    blockLines.push(fwd.substring(consumed + contentIndent, lineEnd))
                    consumed = lineEnd
                    if (consumed < fwd.length && fwd[consumed] === '\r') consumed++
                    if (consumed < fwd.length && fwd[consumed] === '\n') consumed++
                    extraRows++
                  }
                  // Apply chomping.
                  // Remove trailing empty lines for non-keep.
                  if (chomp !== '+') {
                    while (blockLines.length > 0 && blockLines[blockLines.length - 1] === '') blockLines.pop()
                  }
                  if (isFolded) {
                    key = blockLines.join(' ') + '\n'
                  } else {
                    key = blockLines.join('\n') + '\n'
                  }
                  if (chomp === '-') {
                    key = key.replace(/\n$/, '')
                  }
                } else {
                // Scan continuation lines for key (plain scalar multiline).
                while (consumed < fwd.length) {
                  // Skip comment lines.
                  let lineIndent = 0
                  while (consumed + lineIndent < fwd.length && fwd[consumed + lineIndent] === ' ') lineIndent++
                  let afterSpaces = consumed + lineIndent
                  if (afterSpaces < fwd.length && fwd[afterSpaces] === '#') {
                    // Comment line — skip it.
                    while (afterSpaces < fwd.length && fwd[afterSpaces] !== '\n' && fwd[afterSpaces] !== '\r') afterSpaces++
                    beforeNewline = afterSpaces
                    if (afterSpaces < fwd.length && fwd[afterSpaces] === '\r') afterSpaces++
                    if (afterSpaces < fwd.length && fwd[afterSpaces] === '\n') afterSpaces++
                    extraRows++
                    consumed = afterSpaces
                    continue
                  }
                  // Check if this is a continuation of the key (indented more than ?).
                  if (lineIndent > qIndent && fwd[afterSpaces] !== ':' &&
                      fwd[afterSpaces] !== '?' && fwd[afterSpaces] !== '-') {
                    // Continuation line for multiline key.
                    let contEnd = afterSpaces
                    while (contEnd < fwd.length && fwd[contEnd] !== '\n' && fwd[contEnd] !== '\r') {
                      if (fwd[contEnd] === ' ' && fwd[contEnd+1] === '#') break
                      contEnd++
                    }
                    let contText = fwd.substring(afterSpaces, contEnd).replace(/\s+$/, '')
                    if (contText.length > 0) {
                      key += ' ' + contText
                    }
                    consumed = contEnd
                    beforeNewline = consumed
                    if (consumed < fwd.length && fwd[consumed] === '\r') consumed++
                    if (consumed < fwd.length && fwd[consumed] === '\n') consumed++
                    extraRows++
                    continue
                  }
                  break
                }
                }
                // Now check if the next non-comment line starts with `:`.
                let hasValue = false
                let valConsumed = consumed
                {
                  let ci = consumed
                  // Skip leading spaces on the next line.
                  while (ci < fwd.length && fwd[ci] === ' ') ci++
                  if (ci < fwd.length && fwd[ci] === ':' &&
                      (fwd[ci+1] === ' ' || fwd[ci+1] === '\t' || fwd[ci+1] === '\n' ||
                       fwd[ci+1] === '\r' || fwd[ci+1] === undefined)) {
                    // Found `: ` — this key has a value.
                    hasValue = true
                    valConsumed = ci + 1
                    if (fwd[valConsumed] === ' ' || fwd[valConsumed] === '\t') valConsumed++
                  }
                }
                let src = fwd.substring(0, hasValue ? consumed : keyEnd)
                if (hasValue) {
                  pnt.sI += valConsumed
                  pnt.rI += 1 + extraRows
                  let indent = valConsumed - consumed
                  pnt.cI = indent + 1
                  // Check if there's inline content after `: ` on the same line
                  // that looks like a block mapping or sequence (needs #IN context).
                  let nextCh = fwd[valConsumed]
                  let hasInlineContent = nextCh !== undefined &&
                    nextCh !== '\n' && nextCh !== '\r'
                  let needsIndent = false
                  if (hasInlineContent) {
                    let isQuotedOrFlowOrTag = nextCh === '"' || nextCh === "'" ||
                      nextCh === '[' || nextCh === '{' || nextCh === '!'
                    if (!isQuotedOrFlowOrTag) {
                      // Scan line for mapping key indicator (`: ` or `:` at EOL).
                      let le = valConsumed
                      while (le < fwd.length && fwd[le] !== '\n' && fwd[le] !== '\r') le++
                      for (let ri = valConsumed; ri < le; ri++) {
                        if (fwd[ri] === ':') {
                          let nc = fwd[ri + 1]
                          if (nc === ' ' || nc === '\t' || nc === '\n' ||
                              nc === '\r' || nc === undefined || ri + 1 === le) {
                            needsIndent = true
                            break
                          }
                        }
                      }
                      // Also check for sequence indicator (`- `).
                      if (!needsIndent && nextCh === '-' &&
                          (fwd[valConsumed + 1] === ' ' || fwd[valConsumed + 1] === '\t')) {
                        needsIndent = true
                      }
                    }
                  }
                  if (needsIndent) {
                    // Block mapping/sequence inline (e.g., `: get:\n    v: 1`).
                    // Emit CL then IN to establish indent context.
                    let clTkn = lex.token('#CL', 1, ': ', lex.pnt)
                    let inTkn = lex.token('#IN', indent, '', lex.pnt)
                    pendingTokens.push(clTkn, inTkn)
                  } else {
                    // Simple scalar or value on next line.
                    // Just emit CL; the newline handler will emit IN if needed.
                    pendingExplicitCL = true
                  }
                } else {
                  // No `:` follows — don't consume past newline so the
                  // normal newline→#IN handler can emit indent for map continuation.
                  pnt.sI += beforeNewline
                  pnt.cI += beforeNewline
                  // Emit KEY, CL, null as queued tokens.
                  let clTkn = lex.token('#CL', 1, ': ', lex.pnt)
                  let vlTkn = lex.token('#VL', null, '', lex.pnt)
                  pendingTokens.push(clTkn, vlTkn)
                }
                let tkn = lex.token('#TX', key, src, lex.pnt)
                return tkn
              }

              // YAML document-frame markers at column 0:
              //   --- → emit #DS (document start)
              //   ... → emit #DE (document end)
              // The handler also consumes trailing whitespace, optional `#`
              // comment, and the newline ending the marker line — so the next
              // matcher call lands directly on the next document's content
              // (no spurious #IN gets emitted between #DS and the content).
              // Inline content on the same line as the marker (--- foo) is
              // left in place for the next call.
              if ((pnt.sI === 0 || lex.src[pnt.sI - 1] === '\n' ||
                   lex.src[pnt.sI - 1] === '\r') &&
                  ((fwd[0] === '-' && fwd[1] === '-' && fwd[2] === '-' &&
                    (fwd[3] === '\n' || fwd[3] === '\r' ||
                     fwd[3] === ' ' || fwd[3] === '\t' || fwd[3] === undefined)) ||
                   (fwd[0] === '.' && fwd[1] === '.' && fwd[2] === '.' &&
                    (fwd[3] === '\n' || fwd[3] === '\r' ||
                     fwd[3] === ' ' || fwd[3] === '\t' || fwd[3] === undefined)))) {
                let isEnd = fwd[0] === '.'
                let pos = 3
                while (pos < fwd.length && (fwd[pos] === ' ' || fwd[pos] === '\t')) pos++
                let hasInline = pos < fwd.length &&
                  fwd[pos] !== '\n' && fwd[pos] !== '\r' && fwd[pos] !== '#'
                if (!hasInline) {
                  // Skip a trailing comment, then the line terminator.
                  while (pos < fwd.length && fwd[pos] !== '\n' && fwd[pos] !== '\r') pos++
                  if (fwd[pos] === '\r') pos++
                  if (fwd[pos] === '\n') { pos++; pnt.rI++ }
                  pnt.cI = 1  // column 1 at start of next line
                } else {
                  pnt.cI += pos
                }
                pnt.sI += pos
                let tkn = lex.token(isEnd ? '#DE' : '#DS', undefined,
                                    fwd.substring(0, 3), lex.pnt)
                return tkn
              }

              // Non-specific tag.
              if (fwd[0] === '!' && fwd[1] === ' ') {
                let valStart = 2
                let valEnd = valStart
                while (valEnd < fwd.length && fwd[valEnd] !== '\n' && fwd[valEnd] !== '\r') valEnd++
                let rawVal = fwd.substring(valStart, valEnd).replace(/\s+$/, '')
                let src = fwd.substring(0, valEnd)
                let tkn = lex.token('#TX', rawVal, src, lex.pnt)
                pnt.sI += valEnd
                pnt.cI += valEnd
                return tkn
              }
              // Anchor after ---.
              if (fwd[0] === '&') {
                let nameEnd = 1
                while (nameEnd < fwd.length && fwd[nameEnd] !== ' ' && fwd[nameEnd] !== '\t' &&
                       fwd[nameEnd] !== '\n' && fwd[nameEnd] !== '\r' && fwd[nameEnd] !== ',' &&
                       fwd[nameEnd] !== '{' && fwd[nameEnd] !== '}' && fwd[nameEnd] !== '[' &&
                       fwd[nameEnd] !== ']') nameEnd++
                let anchorName = fwd.substring(1, nameEnd)
                let skip = nameEnd
                if (fwd[skip] === ' ') skip++
                pnt.sI += skip
                pnt.cI += skip
                pendingAnchors.push({ name: anchorName, inline: true })
                fwd = lex.refwd()
              }

              // YAML double-quoted string: backslash escapes + multiline folding.
              if (fwd[0] === '"') {
                let i = 1
                let val = ''
                let escapedUpTo = 0 // val chars up to this index are from escapes (non-trimmable)
                while (i < fwd.length && fwd[i] !== '"') {
                  if (fwd[i] === '\\') {
                    i++
                    let esc = fwd[i]
                    if (esc === 'n') { val += '\n'; i++; escapedUpTo = val.length }
                    else if (esc === 't') { val += '\t'; i++; escapedUpTo = val.length }
                    else if (esc === 'r') { val += '\r'; i++; escapedUpTo = val.length }
                    else if (esc === '"') { val += '"'; i++; escapedUpTo = val.length }
                    else if (esc === '\\') { val += '\\'; i++; escapedUpTo = val.length }
                    else if (esc === '/') { val += '/'; i++; escapedUpTo = val.length }
                    else if (esc === 'b') { val += '\b'; i++; escapedUpTo = val.length }
                    else if (esc === 'f') { val += '\f'; i++; escapedUpTo = val.length }
                    else if (esc === 'a') { val += '\x07'; i++; escapedUpTo = val.length }
                    else if (esc === 'e') { val += '\x1b'; i++; escapedUpTo = val.length }
                    else if (esc === 'v') { val += '\v'; i++; escapedUpTo = val.length }
                    else if (esc === '0') { val += '\0'; i++; escapedUpTo = val.length }
                    else if (esc === '\t') { val += '\t'; i++; escapedUpTo = val.length }
                    else if (esc === ' ') { val += ' '; i++; escapedUpTo = val.length }
                    else if (esc === '_') { val += '\u00a0'; i++; escapedUpTo = val.length }
                    else if (esc === 'N') { val += '\u0085'; i++; escapedUpTo = val.length }
                    else if (esc === 'L') { val += '\u2028'; i++; escapedUpTo = val.length }
                    else if (esc === 'P') { val += '\u2029'; i++; escapedUpTo = val.length }
                    else if (esc === 'x') {
                      val += String.fromCharCode(parseInt(fwd.substring(i+1, i+3), 16))
                      i += 3; escapedUpTo = val.length
                    }
                    else if (esc === 'u') {
                      val += String.fromCharCode(parseInt(fwd.substring(i+1, i+5), 16))
                      i += 5; escapedUpTo = val.length
                    }
                    else if (esc === 'U') {
                      val += String.fromCodePoint(parseInt(fwd.substring(i+1, i+9), 16))
                      i += 9; escapedUpTo = val.length
                    }
                    else if (esc === '\n' || esc === '\r') {
                      // Escaped newline: line continuation (join directly).
                      if (esc === '\r' && fwd[i+1] === '\n') i++
                      i++
                      // Skip leading whitespace on next line.
                      while (i < fwd.length && (fwd[i] === ' ' || fwd[i] === '\t')) i++
                    }
                    else { val += esc; i++ }
                  } else if (fwd[i] === '\n' || fwd[i] === '\r') {
                    // Flow scalar line folding for double-quoted strings.
                    // Only trim trailing whitespace that was NOT from escape sequences.
                    let trimTo = val.length
                    while (trimTo > escapedUpTo && (val[trimTo - 1] === ' ' || val[trimTo - 1] === '\t')) trimTo--
                    val = val.substring(0, trimTo)
                    let emptyLines = 0
                    while (i < fwd.length && (fwd[i] === '\n' || fwd[i] === '\r')) {
                      if (fwd[i] === '\r') i++
                      if (fwd[i] === '\n') i++
                      emptyLines++
                      while (i < fwd.length && (fwd[i] === ' ' || fwd[i] === '\t')) i++
                    }
                    if (emptyLines > 1) {
                      for (let e = 1; e < emptyLines; e++) val += '\n'
                    } else {
                      val += ' '
                    }
                  } else {
                    val += fwd[i]
                    i++
                  }
                }
                if (fwd[i] === '"') i++
                let src = fwd.substring(0, i)
                let tkn = lex.token('#ST', val, src, lex.pnt)
                pnt.sI += i
                pnt.cI += i
                return tkn
              }

              // YAML single-quoted string: no backslash escape processing.
              // Only escape is '' (two single quotes) → literal single quote.
              // Newlines are folded: single newline → space, empty lines → \n.
              if (fwd[0] === "'") {
                let i = 1
                let val = ''
                while (i < fwd.length) {
                  if (fwd[i] === "'") {
                    if (fwd[i + 1] === "'") {
                      // Escaped single quote.
                      val += "'"
                      i += 2
                    } else {
                      // End of string.
                      i++
                      break
                    }
                  } else if (fwd[i] === '\n' || fwd[i] === '\r') {
                    // Flow scalar line folding.
                    // Trim trailing whitespace from current content.
                    val = val.replace(/[ \t]+$/, '')
                    // Count empty lines (newlines with only whitespace).
                    let emptyLines = 0
                    while (i < fwd.length && (fwd[i] === '\n' || fwd[i] === '\r')) {
                      if (fwd[i] === '\r') i++
                      if (fwd[i] === '\n') i++
                      emptyLines++
                      // Skip leading whitespace on next line.
                      while (i < fwd.length && (fwd[i] === ' ' || fwd[i] === '\t')) i++
                    }
                    if (emptyLines > 1) {
                      // Each extra empty line becomes a \n.
                      for (let e = 1; e < emptyLines; e++) val += '\n'
                    } else {
                      // Single newline → space (folding).
                      val += ' '
                    }
                  } else {
                    val += fwd[i]
                    i++
                  }
                }
                let src = fwd.substring(0, i)
                let tkn = lex.token('#ST', val, src, lex.pnt)
                pnt.sI += i
                pnt.cI += i
                return tkn
              }

              // Plain scalars starting with digits but containing colons (e.g. 20:03:20),
              // trailing commas (e.g. 12,), or non-numeric text after a space
              // (e.g. "64 characters, hexadecimal.") must be captured before
              // jsonic's number matcher grabs just the digits.
              if (fwd[0] >= '0' && fwd[0] <= '9') {
                updateFlowState(lex.src as string, pnt.sI)
                let inFlow = _flowDepth > 0
                let hasEmbeddedColon = false
                let hasTrailingText = false
                let hasTrailingComma = false
                let pi = 1
                while (pi < fwd.length && fwd[pi] !== '\n' && fwd[pi] !== '\r') {
                  if (fwd[pi] === ':' && fwd[pi + 1] !== ' ' && fwd[pi + 1] !== '\t' &&
                      fwd[pi + 1] !== '\n' && fwd[pi + 1] !== '\r' && fwd[pi + 1] !== undefined) {
                    hasEmbeddedColon = true
                    break
                  }
                  // Trailing comma at end of line means plain scalar in block
                  // context (e.g. "12,"). In flow context commas are always
                  // separators, so don't treat the digits as a plain scalar.
                  if (fwd[pi] === ',') {
                    let ci = pi + 1
                    while (ci < fwd.length && (fwd[ci] === ' ' || fwd[ci] === '\t')) ci++
                    if (!inFlow && (ci >= fwd.length || fwd[ci] === '\n' || fwd[ci] === '\r')) {
                      hasTrailingComma = true
                    }
                    break
                  }
                  if (fwd[pi] === ' ' || fwd[pi] === '\t') {
                    // Check if after the space there are non-separator characters,
                    // meaning this is a plain scalar like "64 characters, hexadecimal."
                    // not a standalone number.
                    let si = pi
                    while (si < fwd.length && (fwd[si] === ' ' || fwd[si] === '\t')) si++
                    if (si < fwd.length && fwd[si] !== '\n' && fwd[si] !== '\r' &&
                        fwd[si] !== '#' && fwd[si] !== ':' && fwd[si] !== undefined) {
                      // Check it's not ": " (key-value separator).
                      if (!(fwd[si] === ':' && (fwd[si + 1] === ' ' || fwd[si + 1] === '\t' ||
                            fwd[si + 1] === '\n' || fwd[si + 1] === '\r' || fwd[si + 1] === undefined))) {
                        hasTrailingText = true
                      }
                    }
                    break
                  }
                  pi++
                }
                if (hasEmbeddedColon || hasTrailingComma) {
                  // Scan to end of plain scalar token (space, tab, newline, eof).
                  let end = 0
                  while (end < fwd.length && fwd[end] !== ' ' && fwd[end] !== '\t' &&
                         fwd[end] !== '\n' && fwd[end] !== '\r') end++
                  let text = fwd.substring(0, end)
                  let tkn = lex.token('#TX', text, text, lex.pnt)
                  pnt.sI += end
                  pnt.cI += end
                  return tkn
                }
                if (hasTrailingText) {
                  // Flag that the number matcher should skip this value,
                  // so the text.check handler can process it as a plain
                  // scalar (including multiline continuation support).
                  skipNumberMatch = true
                  return null
                }
              }

              // YAML element marker: "- " or "-\t" or "-\n" or "-" at end.
              if (fwd[0] === '-' && (fwd[1] === ' ' || fwd[1] === '\t' || fwd[1] === '\n' ||
                  fwd[1] === '\r' || fwd[1] === undefined)) {
                let tkn = lex.token('#EL', undefined, '- ', lex.pnt)
                pnt.sI += 1
                pnt.cI += 1
                // Consume the space/tab after dash if present.
                if (fwd[1] === ' ' || fwd[1] === '\t') {
                  pnt.sI += 1
                  pnt.cI += 1
                }
                return tkn
              }

              // Yaml colons are ': ', ':\t', ':<newline>', ':' at end of input,
              // or ':' in flow context (JSON-compatible, e.g. {"key":value}).
              // In flow context, detect by checking if the previous non-whitespace
              // token was a quoted string followed immediately by ':'.
              let isFlowColon = false
              if (fwd[0] === ':' && fwd[1] !== ' ' && fwd[1] !== '\t' &&
                  fwd[1] !== '\n' && fwd[1] !== '\r' && fwd[1] !== undefined) {
                // JSON-compatible flow colon: only when preceded by a quoted string.
                // Skip whitespace/newlines and any intervening line comments
                // (`# ...` to end-of-line) so e.g. `"foo" # c\n :bar` works.
                let prevI = pnt.sI - 1
                while (prevI >= 0) {
                  let pc = lex.src[prevI]
                  if (pc === ' ' || pc === '\t' || pc === '\n' || pc === '\r') {
                    prevI--; continue
                  }
                  // If on a line whose `#` is preceded by whitespace, that's a
                  // comment — jump to before the `#` and keep walking back.
                  let lineStart = prevI
                  while (lineStart > 0 && lex.src[lineStart - 1] !== '\n' &&
                         lex.src[lineStart - 1] !== '\r') lineStart--
                  let hashAt = -1
                  for (let li = lineStart; li <= prevI; li++) {
                    if (lex.src[li] === '#' &&
                        (li === lineStart || lex.src[li - 1] === ' ' ||
                         lex.src[li - 1] === '\t')) { hashAt = li; break }
                  }
                  if (hashAt >= 0) { prevI = hashAt - 1; continue }
                  break
                }
                if (prevI >= 0 && (lex.src[prevI] === '"' || lex.src[prevI] === "'")) {
                  isFlowColon = true
                }
              }
              if (fwd[0] === ':' && (fwd[1] === ' ' || fwd[1] === '\t' || fwd[1] === '\n' ||
                  fwd[1] === '\r' || fwd[1] === undefined || isFlowColon)) {
                let tkn = lex.token('#CL', 1, ': ', lex.pnt)
                pnt.sI += 1
                if (fwd[1] === ' ' || fwd[1] === '\t') {
                  pnt.cI += 2
                } else if (fwd[1] === '\n' || fwd[1] === '\r') {
                  // Don't consume newline — leave for #IN.
                } else {
                  // End of input after colon.
                  pnt.cI += 1
                }
                return tkn
              }

              // Match any newline — YAML indentation is significant.
              // In flow context, newlines are just whitespace — don't emit #IN.
              if (fwd[0] === '\n' || fwd[0] === '\r') {
                updateFlowState(lex.src as string, pnt.sI)
                if (_flowDepth > 0) {
                  // Inside flow collection — consume whitespace, don't emit #IN.
                  let pos = 0
                  while (pos < fwd.length &&
                    (fwd[pos] === '\n' || fwd[pos] === '\r' || fwd[pos] === ' ' || fwd[pos] === '\t')) {
                    pos++
                  }
                  // Also skip comment lines inside flow collections.
                  if (pos < fwd.length && fwd[pos] === '#') {
                    while (pos < fwd.length && fwd[pos] !== '\n' && fwd[pos] !== '\r') pos++
                  }
                  pnt.sI += pos
                  pnt.cI = 0
                  // Re-run yamlMatcher from new position.
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }
              }
              // Must catch all newlines before the default line/space matchers.
              if (fwd[0] === '\n' || fwd[0] === '\r') {
                // Consume all blank lines and comment-only lines,
                // finding the last meaningful indent.
                let pos = 0
                let spaces = 0
                let rows = 0
                while (pos < fwd.length) {
                  // Match \r\n or \n
                  if (fwd[pos] === '\r' && fwd[pos + 1] === '\n') {
                    pos += 2
                    rows++
                  } else if (fwd[pos] === '\n') {
                    pos += 1
                    rows++
                  } else {
                    break
                  }
                  // Count spaces after this newline.
                  spaces = 0
                  while (pos < fwd.length && fwd[pos] === ' ') {
                    pos++
                    spaces++
                  }
                  // If the line is a comment-only line, consume it too.
                  if (fwd[pos] === '#') {
                    while (pos < fwd.length && fwd[pos] !== '\n' && fwd[pos] !== '\r') pos++
                    continue
                  }
                  // If the line is whitespace-only (tabs and/or spaces),
                  // treat it as a blank line and continue.
                  if (fwd[pos] === '\t') {
                    let tp = pos
                    while (tp < fwd.length && (fwd[tp] === ' ' || fwd[tp] === '\t')) tp++
                    if (tp >= fwd.length || fwd[tp] === '\n' || fwd[tp] === '\r') {
                      pos = tp
                      continue
                    }
                  }
                  // If the line is an anchor-only line (&name with nothing after),
                  // consume it (record the anchor) and continue to the next line
                  // so the indent is determined by actual content.
                  if (fwd[pos] === '&') {
                    let ae = pos + 1
                    while (ae < fwd.length && fwd[ae] !== ' ' && fwd[ae] !== '\t' &&
                           fwd[ae] !== '\n' && fwd[ae] !== '\r') ae++
                    let afterAnchor = ae
                    while (afterAnchor < fwd.length &&
                           (fwd[afterAnchor] === ' ' || fwd[afterAnchor] === '\t')) afterAnchor++
                    if (afterAnchor >= fwd.length || fwd[afterAnchor] === '\n' ||
                        fwd[afterAnchor] === '\r' || fwd[afterAnchor] === '#') {
                      pendingAnchors.push({ name: fwd.substring(pos + 1, ae), inline: false })
                      // Skip to end of line (including any comment).
                      while (afterAnchor < fwd.length &&
                             fwd[afterAnchor] !== '\n' && fwd[afterAnchor] !== '\r') afterAnchor++
                      pos = afterAnchor
                      continue
                    }
                  }
                }

                // If we consumed everything (trailing newlines), advance and emit #ZZ.
                if (pos >= fwd.length) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = spaces + 1
                  let tkn = lex.token('#ZZ', undefined, '', lex.pnt)
                  pnt.end = tkn
                  return tkn
                }

                // If the next line is a document-frame marker (--- / ...) at
                // column 0, advance to it without emitting #IN. The next
                // matcher call will emit the corresponding #DS / #DE token.
                if (spaces === 0 &&
                    ((fwd[pos] === '-' && fwd[pos+1] === '-' && fwd[pos+2] === '-' &&
                      (fwd[pos+3] === '\n' || fwd[pos+3] === '\r' ||
                       fwd[pos+3] === ' ' || fwd[pos+3] === '\t' ||
                       fwd[pos+3] === undefined)) ||
                     (fwd[pos] === '.' && fwd[pos+1] === '.' && fwd[pos+2] === '.' &&
                      (fwd[pos+3] === '\n' || fwd[pos+3] === '\r' ||
                       fwd[pos+3] === ' ' || fwd[pos+3] === '\t' ||
                       fwd[pos+3] === undefined)))) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }

                // Likewise if the next line is a directive (%YAML / %TAG):
                // advance and let the next call emit #DR.
                if (spaces === 0 && fwd[pos] === '%') {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }

                // Skip #IN when the next content is a flow indicator or quoted
                // string at column 0 — there's no block to indent into. Match
                // the previous behavior of the inline `--- foo` handler.
                if (spaces === 0 &&
                    (fwd[pos] === '{' || fwd[pos] === '[' ||
                     fwd[pos] === '"' || fwd[pos] === "'")) {
                  pnt.sI += pos
                  pnt.rI += rows
                  pnt.cI = 0
                  fwd = lex.refwd()
                  continue yamlMatchLoop
                }

                // Emit #IN with val = indent level of the last non-blank line.
                let src = fwd.substring(0, pos)
                let tkn = lex.token('#IN', spaces, src, lex.pnt)
                pnt.sI += pos
                pnt.rI += rows
                pnt.cI = spaces + 1
                return tkn
              }

              break // End of yamlMatchLoop
              } // end while(true) yamlMatchLoop
            }
          }
        }
      }
    }
  })


  // Extract a key value from a token, resolving aliases.
  function extractKey(rule: Rule, tkn: Token = rule.o0): any {
    if (VL === tkn.tin && tkn.val && typeof tkn.val === 'object' && tkn.val.__yamlAlias) {
      // Alias used as key — resolve to anchor value.
      let name = tkn.val.__yamlAlias
      return anchors[name] !== undefined ? anchors[name] : '*' + name
    }
    return ST === tkn.tin || TX === tkn.tin ? tkn.val : tkn.src
  }

  // Function refs used by the declarative grammar (yaml-grammar.jsonic).
  const refs: Record<string, Function> = {
    '@val-indent-deeper': (rule: Rule, ctx: Context) => {
      let parentIn = rule.k.yamlIn
      let listIn = rule.k.yamlListIn
      if (listIn != null && ctx.t0.val <= listIn) return false
      return parentIn == null || ctx.t0.val > parentIn
    },
    '@val-indent-eq-parent': (rule: Rule, ctx: Context) => {
      let parentIn = rule.k.yamlIn
      return parentIn != null && ctx.t0.val === parentIn
    },
    '@val-set-in-from-o0': (rule: Rule) => { rule.n.in = rule.o0.val },
    '@val-set-null': (rule: Rule) => { rule.node = null },
    '@val-set-el-in': (rule: Rule) => { rule.n.in = rule.o0.cI - 1 },
    '@indent-plain-value': (rule: Rule) => {
      rule.node = ST === rule.o0.tin || TX === rule.o0.tin
        ? rule.o0.val : rule.o0.src
    },
    '@set-map-in': (rule: Rule) => { rule.k.yamlMapIn = rule.n.in + 2 },
    '@t0-eq-in': (rule: Rule, ctx: Context) => ctx.t0.val === rule.n.in,
    '@t0-le-in': (rule: Rule, ctx: Context) => ctx.t0.val <= rule.n.in,
    '@t0-lt-in': (rule: Rule, ctx: Context) => ctx.t0.val < rule.n.in,
    '@o0-eq-in': (rule: Rule) => rule.o0.val === rule.n.in,
    '@t0-eq-map-in': (rule: Rule, ctx: Context) => ctx.t0.val === rule.k.yamlMapIn,
    '@elem-key': (rule: Rule) => { rule.u.key = extractKey(rule) },
    '@implicit-null-pair': (rule: Rule) => {
      let key = extractKey(rule)
      rule.u.key = key
      rule.node[key] = null
    },
    // Same as @pairkey, but the KEY is at o1 (after the leading #QM).
    '@qm-pairkey': (rule: Rule) => { rule.u.key = extractKey(rule, rule.o1) },
    '@qm-implicit-null-pair': (rule: Rule) => {
      let key = extractKey(rule, rule.o1)
      rule.u.key = key
      rule.node[key] = null
    },
  }

  // Parse the embedded grammar text and install declarative rules.
  const grammarDef: any = (Jsonic.make() as any)(grammarText)
  grammarDef.ref = refs
  jsonic.grammar(grammarDef)

  // ===== State handlers (bo/ao/bc/ac) — kept in code for closure capture =====

  // val rule: claim pending anchors (ao), handle empty (bc),
  //          resolve aliases and record anchors (ac).
  jsonic.rule('val', (rulespec: RuleSpec) => {
    rulespec.ao((rule: Rule) => {
      if (pendingAnchors.length > 0) {
        rule.u.yamlAnchors = [...pendingAnchors]
        rule.u.yamlAnchorOpenNode = rule.node
        pendingAnchors.length = 0
      }
    })
    rulespec.bc((rule: Rule) => {
      if (rule.u.yamlEmpty) {
        rule.node = undefined
      }
    })
    rulespec.ac((rule: Rule) => {
      // Resolve alias markers to actual values.
      if (rule.node && typeof rule.node === 'object' &&
          rule.node.__yamlAlias) {
        let name = rule.node.__yamlAlias
        let val = anchors[name]
        if (typeof val === 'object' && val !== null) {
          rule.node = JSON.parse(JSON.stringify(val))
        } else {
          rule.node = val
        }
      }

      // Record anchors only if this val claimed them.
      if (rule.u.yamlAnchors) {
        for (let anchor of rule.u.yamlAnchors) {
          if (anchor.inline &&
              rule.u.yamlAnchorOpenNode != null &&
              typeof rule.u.yamlAnchorOpenNode !== 'object' &&
              typeof rule.node === 'object' && rule.node !== null) {
            continue
          }
          let val = rule.node
          if (typeof val === 'object' && val !== null) {
            val = JSON.parse(JSON.stringify(val))
          }
          anchors[anchor.name] = val
        }
      }
    })
  })

  // indent rule: propagate child node up on close.
  jsonic.rule('indent', (rulespec: RuleSpec) => {
    rulespec.bc((rule: Rule) => {
      if (undefined !== rule.child.node) {
        rule.node = rule.child.node
      }
    })
  })

  // yamlBlockList rule: init array and push child nodes.
  jsonic.rule('yamlBlockList', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = []
      rule.k.yamlBlockArr = rule.node
      rule.k.yamlListIn = rule.n.in
    })
    rulespec.bc((rule: Rule) => {
      let val = rule.child.node !== undefined ? rule.child.node : null
      rule.k.yamlBlockArr.push(val)
    })
  })

  // yamlBlockElem rule: reuse shared array, push child nodes.
  jsonic.rule('yamlBlockElem', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = rule.k.yamlBlockArr
    })
    rulespec.bc((rule: Rule) => {
      let val = rule.child.node !== undefined ? rule.child.node : null
      rule.k.yamlBlockArr.push(val)
    })
  })

  // list rule: propagate list indent so val can check nesting depth.
  jsonic.rule('list', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.k.yamlListIn = rule.n.in
    })
  })

  // ===== stream rule: top-level YAML document collector =====
  // The stream rule replaces `val` as the parser's start rule. It consumes
  // doc-frame tokens (#DS, #DE, #DR) emitted by yamlMatcher, pushes a fresh
  // `val` rule for each document's content, and accumulates the results.
  // Final shape:
  //   - 0 docs (empty source) → undefined
  //   - 1 doc                  → the single value
  //   - >1 docs                → array of values
  const ensureCurMeta = () => {
    if (!yamlStreamCurMeta) {
      yamlStreamCurMeta = { directives: [], explicit: false, ended: false }
    }
  }
  const flushCurMeta = (ended: boolean) => {
    ensureCurMeta()
    yamlStreamCurMeta!.ended = ended || yamlStreamCurMeta!.ended
    yamlStreamMeta.push(yamlStreamCurMeta!)
    yamlStreamCurMeta = null
  }
  const accumChildDoc = (rule: Rule) => {
    if (rule.child && rule.child.node !== undefined) {
      yamlStreamDocs.push(rule.child.node)
    } else {
      yamlStreamDocs.push(null)
    }
    // The matched close-phase token tells us whether this doc ended
    // explicitly with `...`.
    flushCurMeta(rule.c0 != null && rule.c0.tin === DE)
  }
  const finalizeStream = (rule: Rule, ctx: Context) => {
    if (rule.child && rule.child.node !== undefined) {
      yamlStreamDocs.push(rule.child.node)
      flushCurMeta(false)
    }
    let content: any
    if (yamlStreamDocs.length === 0) {
      content = undefined
    } else if (yamlStreamDocs.length === 1) {
      content = yamlStreamDocs[0]
    } else {
      content = yamlStreamDocs.slice()
    }
    let result: any = content
    if (options.meta) {
      let meta: any
      if (yamlStreamMeta.length === 0) {
        meta = undefined
      } else if (yamlStreamMeta.length === 1) {
        meta = yamlStreamMeta[0]
      } else {
        meta = yamlStreamMeta.slice()
      }
      result = { meta, content }
    }
    rule.node = result
    // Rotation via `r: stream` creates a chain; ctx.root() is the original
    // stream the parser hands back. Write the result there.
    ctx.root().node = result
  }
  const applyDirective = (rule: Rule) => {
    let src: string = rule.o0.src || ''
    let m = src.match(/^%TAG\s+(\S+)\s+(\S+)/)
    if (m) tagHandles[m[1]] = m[2]
    ensureCurMeta()
    yamlStreamCurMeta!.directives.push(src)
  }
  const markExplicit = (_rule: Rule) => {
    ensureCurMeta()
    yamlStreamCurMeta!.explicit = true
  }
  const pushEmptyDoc = (_rule: Rule) => {
    yamlStreamDocs.push(null)
    flushCurMeta(true)
  }

  jsonic.rule('stream', (rs: RuleSpec) => {
    rs.open([
      // Consume directive line; rotate to stream to look for the next token.
      { s: '#DR', a: applyDirective, r: 'stream', g: 'yaml' },
      // Explicit doc start: push val for the document content.
      { s: '#DS', a: markExplicit, p: 'val', g: 'yaml' },
      // ... before any content: count as empty doc, look for more.
      { s: '#DE', a: pushEmptyDoc, r: 'stream', g: 'yaml' },
      // Empty source: end immediately (stream.close will run).
      { s: '#ZZ', b: 1, g: 'yaml' },
      // Implicit first doc.
      { p: 'val', g: 'yaml' },
    ])
    rs.close([
      // End of input: accumulate last doc, finalize result shape.
      { s: '#ZZ', a: finalizeStream, g: 'yaml' },
      // Directive between docs: accumulate previous doc, apply, continue.
      { s: '#DR', a: (r: Rule) => { accumChildDoc(r); applyDirective(r) },
        r: 'stream', g: 'yaml' },
      // ... terminator: accumulate, look for next doc.
      { s: '#DE', a: accumChildDoc, r: 'stream', g: 'yaml' },
      // --- start of next doc (back up so stream.open consumes it).
      { s: '#DS', b: 1, a: accumChildDoc, r: 'stream', g: 'yaml' },
    ])
  })

  // Configure jsonic to start parsing with `stream` instead of `val`.
  jsonic.options({ rule: { start: 'stream' } })

  // map rule: default indent and merge-key handling.
  jsonic.rule('map', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      if (null == rule.n.in) {
        rule.n.in = 0
      }
      rule.k.yamlIn = rule.n.in
    })
    rulespec.ac((rule: Rule) => {
      if (rule.node && typeof rule.node === 'object' && '<<' in rule.node) {
        let mergeVal = rule.node['<<']
        delete rule.node['<<']
        if (Array.isArray(mergeVal)) {
          for (let m of mergeVal) {
            if (typeof m === 'object' && m !== null && !Array.isArray(m)) {
              for (let k of Object.keys(m)) {
                if (!(k in rule.node)) rule.node[k] = m[k]
              }
            }
          }
        } else if (typeof mergeVal === 'object' && mergeVal !== null) {
          for (let k of Object.keys(mergeVal)) {
            if (!(k in rule.node)) rule.node[k] = mergeVal[k]
          }
        }
      }
    })
  })

  // yamlElemMap rule: init map and store pairs.
  jsonic.rule('yamlElemMap', (rulespec: RuleSpec) => {
    rulespec.bo((rule: Rule) => {
      rule.node = Object.create(null)
    })
    rulespec.bc((rule: Rule) => {
      if (rule.u.key != null) {
        rule.node[rule.u.key] = rule.child.node
      }
    })
  })

  // yamlElemPair rule: store pair into shared map node.
  jsonic.rule('yamlElemPair', (rulespec: RuleSpec) => {
    rulespec.bc((rule: Rule) => {
      if (rule.u.key != null) {
        rule.node[rule.u.key] = rule.child.node
      }
    })
  })

}


Yaml.defaults = ({
  meta: false,
} as YamlOptions)


export {
  Yaml,
}

export type {
  YamlOptions,
}
