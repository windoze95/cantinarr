package auth

import (
	_ "embed"
	"encoding/base64"
	"html/template"
)

// The browser auth pages also ship in the server-only image, without Flutter
// web assets. Keep their shared styling and branding self-contained.
//
//go:embed auth_page.css
var authPageCSS string

// auth_logo.png is app/web/icons/Icon-192.png; copy it when the app logo changes.
//
//go:embed auth_logo.png
var authPageLogo []byte

var authPagePartials = `
{{define "auth-style"}}<style>` + authPageCSS + `</style>{{end}}
{{define "auth-brand"}}
    <div class="brand">
      <div class="brand-icon"><img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString(authPageLogo) + `" width="72" height="72" alt=""></div>
      <div class="brand-name">CANTINARR</div>
    </div>
{{end}}`

func newAuthPageTemplate(name, page string) *template.Template {
	return template.Must(template.New(name).Parse(authPagePartials + page))
}
