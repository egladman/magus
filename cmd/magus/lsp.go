package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/internal/langservice"
)

// lspCmd implements `magus buzz lsp`: a stdio Language Server that exposes the
// completion, hover, and signature-help analysis in internal/langservice to any
// editor with a generic LSP client. The analysis engine already exists and is
// tested; this is the transport that lets an editor reach it, so magusfile
// authoring gets the same explicitness at edit time that MAGUS.md gives at read
// time. It lives under `buzz` (the Buzz-language tooling group, not a top-level
// `magus lsp`) so serving other languages later needs no new subcommand contract.
// It speaks JSON-RPC 2.0 over the LSP base protocol (Content-Length framed) on
// stdin/stdout and needs no workspace, config, or daemon: every request is
// answered from the document text the editor sends.
func lspCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("buzz lsp", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus buzz lsp")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Run the magusfile language server over stdio (JSON-RPC 2.0 / LSP).")
		fmt.Fprintln(os.Stderr, "Point a generic LSP client at `magus buzz lsp` for the *.buzz language")
		fmt.Fprintln(os.Stderr, "id; it serves completion, hover, and signature help. See docs/editor.md.")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	return (&lspServer{docs: map[string]string{}}).serve(os.Stdin, os.Stdout)
}

// lspServer holds the open-document buffers the editor syncs to us. The protocol
// is handled one message at a time on a single goroutine, which is well within
// what a language server this size needs, so the buffer map needs no locking.
type lspServer struct {
	docs     map[string]string // uri -> full document text (full-sync)
	shutdown bool              // a shutdown request was received; exit expects it
	writeBuf *bufio.Writer
}

// --- LSP wire types (only the fields we read or send) ---

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type positionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

// serve runs the read/dispatch loop until the stream ends or `exit` is received.
// The exit code convention is the LSP one: 0 if a shutdown request preceded exit,
// 1 otherwise.
func (s *lspServer) serve(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	s.writeBuf = bufio.NewWriter(out)
	for {
		body, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("lsp: read: %w", err)
		}
		var req rpcRequest
		if err := json.Unmarshal(body, &req); err != nil {
			continue // malformed frame: skip rather than kill the session
		}
		if req.Method == "exit" {
			if s.shutdown {
				return nil
			}
			return errSilent{exitCode: 1}
		}
		s.handle(req)
	}
}

// handle dispatches one message. A request (with an id) always gets a response; a
// notification (no id) never does. Unknown requests get MethodNotFound so the
// editor is not left waiting.
func (s *lspServer) handle(req rpcRequest) {
	switch req.Method {
	case "initialize":
		s.reply(req.ID, s.capabilities())
	case "initialized":
		// notification, nothing to do
	case "shutdown":
		s.shutdown = true
		s.reply(req.ID, nil)
	case "textDocument/didOpen":
		var p didOpenParams
		if json.Unmarshal(req.Params, &p) == nil {
			s.docs[p.TextDocument.URI] = p.TextDocument.Text
		}
	case "textDocument/didChange":
		var p didChangeParams
		if json.Unmarshal(req.Params, &p) == nil && len(p.ContentChanges) > 0 {
			// Full sync (we advertise TextDocumentSyncKind.Full): the last change
			// carries the whole document.
			s.docs[p.TextDocument.URI] = p.ContentChanges[len(p.ContentChanges)-1].Text
		}
	case "textDocument/didClose":
		var p didCloseParams
		if json.Unmarshal(req.Params, &p) == nil {
			delete(s.docs, p.TextDocument.URI)
		}
	case "textDocument/completion":
		s.reply(req.ID, s.completion(req.Params))
	case "textDocument/hover":
		s.reply(req.ID, s.hover(req.Params))
	case "textDocument/signatureHelp":
		s.reply(req.ID, s.signatureHelp(req.Params))
	default:
		if len(req.ID) > 0 {
			s.replyError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func (s *lspServer) capabilities() any {
	return map[string]any{
		"capabilities": map[string]any{
			"textDocumentSync": 1, // Full: didChange sends the whole document
			"completionProvider": map[string]any{
				"triggerCharacters": []string{".", "/"},
			},
			"hoverProvider": true,
			"signatureHelpProvider": map[string]any{
				"triggerCharacters": []string{"(", ","},
			},
		},
		"serverInfo": map[string]any{"name": "magus", "version": version},
	}
}

// srcAndOffset resolves a position request to the document text and the byte
// offset the langservice functions take. ok is false when the document is not
// open, so the caller returns an empty result.
func (s *lspServer) srcAndOffset(raw json.RawMessage) (src string, offset int, ok bool) {
	var p positionParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", 0, false
	}
	src, ok = s.docs[p.TextDocument.URI]
	if !ok {
		return "", 0, false
	}
	return src, positionToOffset(src, p.Position.Line, p.Position.Character), true
}

func (s *lspServer) completion(raw json.RawMessage) any {
	src, offset, ok := s.srcAndOffset(raw)
	if !ok {
		return map[string]any{"isIncomplete": false, "items": []any{}}
	}
	items := make([]map[string]any, 0)
	for _, c := range langservice.CompleteAt(src, offset) {
		item := map[string]any{
			"label": c.Label,
			"kind":  completionItemKind(c.Kind),
		}
		if c.Detail != "" {
			item["detail"] = c.Detail
		}
		if c.Doc != "" {
			item["documentation"] = c.Doc
		}
		// Replace the partial token already typed before the cursor, so accepting a
		// suggestion overwrites it instead of duplicating the prefix.
		if c.Replace > 0 {
			startLine, startCh := offsetToPosition(src, offset-c.Replace)
			item["textEdit"] = map[string]any{
				"range": map[string]any{
					"start": map[string]any{"line": startLine, "character": startCh},
					"end":   posMap(src, offset),
				},
				"newText": c.Label,
			}
		}
		items = append(items, item)
	}
	return map[string]any{"isIncomplete": false, "items": items}
}

func (s *lspServer) hover(raw json.RawMessage) any {
	src, offset, ok := s.srcAndOffset(raw)
	if !ok {
		return nil
	}
	h := langservice.HoverAt(src, offset)
	if h == nil {
		return nil
	}
	value := "```buzz\n" + h.Title + "\n```"
	if h.Doc != "" {
		value += "\n\n" + h.Doc
	}
	return map[string]any{
		"contents": map[string]any{"kind": "markdown", "value": value},
	}
}

func (s *lspServer) signatureHelp(raw json.RawMessage) any {
	src, offset, ok := s.srcAndOffset(raw)
	if !ok {
		return nil
	}
	sig := langservice.SignatureAt(src, offset)
	if sig == nil {
		return nil
	}
	signature := map[string]any{"label": sig.Label}
	if sig.Doc != "" {
		signature["documentation"] = sig.Doc
	}
	return map[string]any{
		"signatures":      []any{signature},
		"activeSignature": 0,
		"activeParameter": 0,
	}
}

// completionItemKind maps a langservice CompletionKind to the LSP
// CompletionItemKind enum so the editor shows the right icon.
func completionItemKind(k langservice.CompletionKind) int {
	switch k {
	case langservice.KindModule:
		return 9 // Module
	case langservice.KindMethod:
		return 2 // Method
	case langservice.KindField:
		return 5 // Field
	case langservice.KindFunction:
		return 3 // Function
	case langservice.KindConstant:
		return 21 // Constant
	case langservice.KindType:
		return 7 // Class
	case langservice.KindKeyword:
		return 14 // Keyword
	default:
		return 1 // Text
	}
}

// --- position <-> byte offset (LSP positions count UTF-16 code units) ---

// positionToOffset converts an LSP (line, character) position to a byte offset in
// text. LSP characters are UTF-16 code units; magusfiles are usually ASCII, but
// the conversion is UTF-16 aware so a non-ASCII comment cannot desync the cursor.
func positionToOffset(text string, line, character int) int {
	off := 0
	for l := 0; l < line; l++ {
		idx := strings.IndexByte(text[off:], '\n')
		if idx < 0 {
			return len(text)
		}
		off += idx + 1
	}
	u16 := 0
	i := off
	for i < len(text) && u16 < character {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\n' {
			break
		}
		i += size
		if r > 0xFFFF {
			u16 += 2 // a surrogate pair
		} else {
			u16++
		}
	}
	return i
}

// offsetToPosition is the inverse: a byte offset to an LSP (line, character) with
// character in UTF-16 code units. Used to build a completion's replace range.
func offsetToPosition(text string, offset int) (line, character int) {
	if offset > len(text) {
		offset = len(text)
	}
	if offset < 0 {
		offset = 0
	}
	lineStart := 0
	for i := 0; i < offset; {
		r, size := utf8.DecodeRuneInString(text[i:])
		if r == '\n' {
			line++
			i += size
			lineStart = i
			continue
		}
		i += size
	}
	// UTF-16 units from the line start to the offset.
	for i := lineStart; i < offset; {
		r, size := utf8.DecodeRuneInString(text[i:])
		i += size
		if r > 0xFFFF {
			character += 2
		} else {
			character++
		}
	}
	return line, character
}

func posMap(text string, offset int) map[string]any {
	l, c := offsetToPosition(text, offset)
	return map[string]any{"line": l, "character": c}
}

// --- JSON-RPC base protocol (Content-Length framed) ---

func (s *lspServer) reply(id json.RawMessage, result any) {
	if len(id) == 0 {
		return // a notification does not get a response
	}
	s.writeMessage(rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *lspServer) replyError(id json.RawMessage, code int, msg string) {
	s.writeMessage(rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *lspServer) writeMessage(v any) {
	body, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "lsp: marshal: %v\n", err)
		return
	}
	fmt.Fprintf(s.writeBuf, "Content-Length: %d\r\n\r\n", len(body))
	if _, err := s.writeBuf.Write(body); err != nil {
		fmt.Fprintf(os.Stderr, "lsp: write: %v\n", err)
		return
	}
	_ = s.writeBuf.Flush()
}

// readMessage reads one LSP base-protocol frame: a set of `Header: value` lines
// terminated by a blank line, then Content-Length bytes of JSON body.
func readMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := -1
	sawHeader := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if !sawHeader {
				continue // tolerate stray blank lines before a frame's headers
			}
			break // blank line after headers ends the header block
		}
		sawHeader = true
		name, value, found := strings.Cut(line, ":")
		if found && strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, convErr := strconv.Atoi(strings.TrimSpace(value))
			if convErr != nil {
				return nil, fmt.Errorf("lsp: bad Content-Length %q: %w", value, convErr)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("lsp: message without Content-Length")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
