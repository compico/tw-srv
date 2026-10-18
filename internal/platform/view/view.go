// Package view: интеграция с Vite для fiber.
//
// Go владеет HTML-оболочкой, а Vite даёт только теги для <head>:
//   - dev:  теги ведут на dev-сервер Vite (HMR);
//   - prod: имена файлов с хэшами берутся из dist/.vite/manifest.json.
//
// https://vite.dev/guide/backend-integration.html
package view

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"path"
	"path/filepath"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/olivere/vite"

	"github.com/compico/tw-srv/frontend"
)

// entryPoint должен совпадать с build.rollupOptions.input в vite.config.ts.
const entryPoint = "src/main.ts"

var shell = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>{{.Title}}</title>
  {{.ViteTags}}
</head>
<body>
  <div id="app"></div>
  <footer class="site-footer">
    Открытый исходный код:
    <a href="https://github.com/compico/tw-srv" target="_blank" rel="noopener">github.com/compico/tw-srv</a>
  </footer>
</body>
</html>`))

type Config struct {
	Dev    bool
	DevURL string // адрес dev-сервера Vite, открываемый браузером
	Title  string
}

type View struct {
	dev    bool
	page   []byte
	assets fs.FS
}

func New(cfg Config) (*View, error) {
	vc := vite.Config{IsDev: cfg.Dev, ViteURL: cfg.DevURL, ViteEntry: entryPoint}

	dist, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("view: sub fs: %w", err)
	}
	if !cfg.Dev {
		vc.FS = dist
	}
	frag, err := vite.HTMLFragment(vc)
	if err != nil {
		return nil, fmt.Errorf("view: %w (собран ли фронтенд? npm run build)", err)
	}

	// Теги статичны на всё время жизни процесса, поэтому оболочку рендерим один раз.
	var buf bytes.Buffer
	if err := shell.Execute(&buf, map[string]any{"Title": cfg.Title, "ViteTags": frag.Tags}); err != nil {
		return nil, fmt.Errorf("view: render: %w", err)
	}
	return &View{dev: cfg.Dev, page: buf.Bytes(), assets: dist}, nil
}

// IndexHandler отдаёт оболочку SPA для любого пути, не занятого другими обработчиками.
func (v *View) IndexHandler(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
	c.Set(fiber.HeaderCacheControl, "no-cache")
	return c.Send(v.page)
}

// AssetsHandler отдаёт файлы бандла из embed.FS (/assets/*). В dev их раздаёт сам Vite.
func (v *View) AssetsHandler(c fiber.Ctx) error {
	// path.Clean от rooted-пути не даёт выйти за пределы корня через "..".
	name := strings.TrimPrefix(path.Clean("/"+c.Path()), "/")
	if !strings.HasPrefix(name, "assets/") {
		return c.SendStatus(fiber.StatusNotFound)
	}
	data, err := fs.ReadFile(v.assets, name)
	if err != nil {
		return c.SendStatus(fiber.StatusNotFound)
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		c.Set(fiber.HeaderContentType, ct)
	}
	// Имена содержат хэш содержимого, поэтому кэшируем навсегда.
	c.Set(fiber.HeaderCacheControl, "public, max-age=31536000, immutable")
	return c.Send(data)
}
