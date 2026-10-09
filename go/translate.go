package tabnasyaml

import _ "embed"

// TranslationPart is one optional alchemy source and the entry point a host
// calls. Source is empty for an entry supplied by alchemy itself.
type TranslationPart struct {
	Entry  string
	Source string
}

// TranslationParts is the package-local structural translation interface.
type TranslationParts struct {
	Manifest string
	Lift     *TranslationPart
	Embed    *TranslationPart
	Render   *TranslationPart
}

//go:embed translate/manifest.json
var translationManifest string

//go:embed translate/render.alc
var translationRender string

var translationParts = TranslationParts{
	Manifest: translationManifest,
	Render: &TranslationPart{
		Entry:  "yaml-render",
		Source: translationRender,
	},
}

// Translate returns YAML's immutable translation parts. The returned value
// must be treated as package data and not modified.
func Translate() *TranslationParts {
	return &translationParts
}
