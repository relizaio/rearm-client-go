package rearm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

// FilePart is one file of a multipart GraphQL request (github.com/jaydenseric/graphql-multipart-request-spec):
// the bytes land in the variable at VariablePath, e.g. "variables.releaseInputProg.artifacts.0.file".
type FilePart struct {
	Filename     string
	Content      io.Reader
	VariablePath string
}

// UploadMultipart sends an operation whose variables carry files. Returns the raw `data` member like Raw.
func UploadMultipart(ctx context.Context, c *Client, opName, query string, variables any, files []FilePart) (json.RawMessage, error) {
	ops, err := json.Marshal(map[string]any{"operationName": opName, "query": query, "variables": variables})
	if err != nil {
		return nil, err
	}
	fileMap := map[string][]string{}
	for i, f := range files {
		fileMap[fmt.Sprint(i)] = []string{f.VariablePath}
	}
	mapJSON, _ := json.Marshal(fileMap)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("operations", string(ops)); err != nil {
		return nil, err
	}
	if err := w.WriteField("map", string(mapJSON)); err != nil {
		return nil, err
	}
	for i, f := range files {
		part, err := w.CreateFormFile(fmt.Sprint(i), f.Filename)
		if err != nil {
			return nil, err
		}
		if _, err := io.Copy(part, f.Content); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	// the CSRF-prevention preflight header the multipart handler requires
	req.Header.Set("Apollo-Require-Preflight", "true")
	return c.exchange(req)
}
