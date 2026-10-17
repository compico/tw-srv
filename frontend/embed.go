// Package frontend встраивает собранный Vite-бандл в бинарник сервиса memealerts.
package frontend

import "embed"

// Dist: результат `npm run build`. В dist/ всегда лежит .gitkeep,
// иначе go:embed не скомпилируется на чистом клоне.
//
//go:embed all:dist
var Dist embed.FS
