package main

import (
	"log"

	dbxpluginsdk "github.com/t8y2/dbx/plugins/sdk/go/dbx-plugin-sdk"

	"github.com/w89588/dbx-plugin-wsl/internal/execx"
	"github.com/w89588/dbx-plugin-wsl/internal/handler"
)

func main() {
	metadata := dbxpluginsdk.Metadata{
		ID:           "com.dbx.wsl",
		Version:      "0.1.5",
		Capabilities: []string{"events", "jobs", "mcp"},
	}
	server := dbxpluginsdk.NewServer(metadata, handler.New(execx.NewRunner()))
	if err := server.Serve(); err != nil {
		log.Fatal(err)
	}
}
