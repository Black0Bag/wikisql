package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func main() {
	base := "http://" + os.Args[1]
	token := os.Getenv("WIKISQL_AUTH_API_KEY")

	client := mcp.NewClient(&mcp.Implementation{Name: "schema-dump", Version: "1.0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   base + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: token}, Timeout: 15 * time.Second},
	}, nil)
	if err != nil {
		panic(err)
	}
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		panic(err)
	}
	for _, t := range tools.Tools {
		b, _ := json.MarshalIndent(t.InputSchema, "", "  ")
		fmt.Printf("=== %s ===\n%s\n\n", t.Name, string(b))
	}
}
