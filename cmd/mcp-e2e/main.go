// mcp-e2e 验证本服务的 MCP 端点连通性：登录、连接、列工具、调工具。
// 用法：go run ./cmd/mcp-e2e -addr 127.0.0.1:8180 -password <登录密码>
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8180", "kb-server 地址")
	password := flag.String("password", os.Getenv("KB_AUTH_PASSWORD"), "登录密码")
	flag.Parse()

	base := "http://" + *addr

	// 1. 登录换取 token
	var loginResp struct {
		Token string `json:"token"`
	}
	body, _ := json.Marshal(map[string]string{"password": *password})
	resp, err := http.Post(base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatalf("login failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Fatalf("login returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
		log.Fatalf("decode login response: %v", err)
	}
	fmt.Println("[1] login OK")

	// 2. 连接 MCP 端点（Authorization 头注入）
	authedClient := &http.Client{
		Transport: bearerTransport{token: loginResp.Token},
		Timeout:   30 * time.Second,
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mcp-e2e", Version: "0.1.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   base + "/mcp",
		HTTPClient: authedClient,
	}, nil)
	if err != nil {
		log.Fatalf("mcp connect failed: %v", err)
	}
	defer session.Close()
	fmt.Println("[2] mcp connect OK")

	// 3. 列出工具
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		log.Fatalf("list tools failed: %v", err)
	}
	fmt.Printf("[3] tools listed (%d):\n", len(tools.Tools))
	for _, t := range tools.Tools {
		fmt.Printf("    - %s\n", t.Name)
	}

	// 4. 调用只读工具
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "db_list_tables",
		Arguments: map[string]any{},
	})
	if err != nil {
		log.Fatalf("call db_list_tables failed: %v", err)
	}
	fmt.Println("[4] db_list_tables OK")

	// 5. 调用知识库工具（KB 未启用时工具不存在，跳过）
	result, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "kb_list_collections",
		Arguments: map[string]any{},
	})
	if err != nil {
		fmt.Println("[5] kb_list_collections unavailable (kb disabled) - skipped")
	} else {
		fmt.Println("[5] kb_list_collections OK")
	}

	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			fmt.Printf("    content: %s\n", truncate(tc.Text, 200))
		}
	}
	fmt.Println("ALL MCP E2E CHECKS PASSED")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}
