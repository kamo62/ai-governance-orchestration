package mcpgateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// StdioTransport runs the MCP server over stdin/stdout.
type StdioTransport struct {
	mcp *Server
}

// NewStdioTransport creates a new stdio transport.
func NewStdioTransport(mcp *Server) *StdioTransport {
	return &StdioTransport{mcp: mcp}
}

// Run starts reading from stdin and writing responses to stdout.
func (t *StdioTransport) Run(ctx context.Context) error {
	return NewReadWriteTransport(t.mcp, os.Stdin, os.Stdout).Run(ctx)
}

// ReadWriteTransport is an MCP transport over arbitrary io.Reader/io.Writer.
type ReadWriteTransport struct {
	mcp    *Server
	reader *bufio.Reader
	writer io.Writer
}

// NewReadWriteTransport creates a transport over arbitrary reader/writer.
func NewReadWriteTransport(mcp *Server, r io.Reader, w io.Writer) *ReadWriteTransport {
	return &ReadWriteTransport{
		mcp:    mcp,
		reader: bufio.NewReader(r),
		writer: w,
	}
}

// Run processes requests from the reader and writes responses to the writer.
func (t *ReadWriteTransport) Run(ctx context.Context) error {
	for {
		line, eof, err := readJSONRPCLine(t.reader)
		if err != nil {
			return err
		}
		if line == "" {
			if eof {
				return nil
			}
			continue
		}

		var req Request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			t.writeResponse(errorResponse(nil, ErrParseError, "parse error"))
			continue
		}

		resp := t.mcp.Handle(ctx, &req)
		if resp != nil {
			t.writeResponse(resp)
		}
		if eof {
			return nil
		}
	}
}

func (t *ReadWriteTransport) writeResponse(resp *Response) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintln(t.writer, string(data))
}

func readJSONRPCLine(reader *bufio.Reader) (line string, eof bool, err error) {
	line, err = reader.ReadString('\n')
	if err != nil {
		if err != io.EOF {
			return "", false, err
		}
		if line == "" {
			return "", true, nil
		}
		eof = true
	}
	line = strings.TrimSuffix(line, "\n")
	line = strings.TrimSuffix(line, "\r")
	return line, eof, nil
}
