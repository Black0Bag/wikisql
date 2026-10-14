package httpapi

import (
	"embed"
	"encoding/json"
	"net/http"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed openapi.yaml
var openapiYAML string

//go:embed swaggerui/.keep
var swaggerUI embed.FS

// ServeOpenAPI 把 YAML 规范转成 JSON 输出在 /openapi.json。
func ServeOpenAPI(w http.ResponseWriter, r *http.Request) {
	var spec any
	if err := yaml.Unmarshal([]byte(openapiYAML), &spec); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "invalid openapi spec"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(spec)
}

// ServeDocs 返回 Swagger UI 页面。UI 资源来自公共 CDN；离线环境下
// /openapi.json 规范文件仍可直接导入任意本地 OpenAPI 工具。
func ServeDocs(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/docs/")
	path = strings.TrimPrefix(path, "/")
	switch {
	case path == "" || path == "docs":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(docsIndexHTML))
	case strings.HasPrefix(path, "swaggerui/"):
		data, err := swaggerUI.ReadFile(path)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Write(data)
	default:
		http.NotFound(w, r)
	}
}

const docsIndexHTML = `<!DOCTYPE html>
<html lang="zh">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>wikisql API 文档</title>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui.css">
<style>body { margin: 0; }</style>
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5.18.2/swagger-ui-bundle.js"></script>
<script>
window.onload = () => {
  window.ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: "#swagger-ui",
    docExpansion: "list",
  });
};
</script>
</body>
</html>`
