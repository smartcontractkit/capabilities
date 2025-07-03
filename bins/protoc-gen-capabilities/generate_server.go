package main

import (
	_ "embed"
	"errors"
	"fmt"

	"github.com/smartcontractkit/chainlink-common/pkg/capabilities/v2/protoc/pkg"
	"google.golang.org/protobuf/compiler/protogen"
)

type serverArgs struct {
	*protogen.File
}

type ServerLanguage string

const (
	ServerLangaugeGo ServerLanguage = "go"
)

func (s ServerLanguage) Validate() error {
	switch s {
	case ServerLangaugeGo:
		return nil
	default:
		return errors.New("unsupported server language")
	}
}

//go:embed templates/server.go.tmpl
var goServerTemplate string

var serverTemplates = map[ServerLanguage]pkg.TemplateGenerator{
	ServerLangaugeGo: {
		Name:             "go_server",
		Template:         goServerTemplate,
		FileNameTemplate: "{{.}}_server_gen.go",
	},
}

func GenerateServer(plugin *protogen.Plugin, file *protogen.File, serverLanguage ServerLanguage) error {
	if len(file.Services) == 0 {
		return nil
	}

	template, ok := serverTemplates[serverLanguage]
	if !ok {
		return fmt.Errorf("unsupported server language: %s", serverLanguage)
	}

	args := serverArgs{File: file}
	return template.GenerateFile(file, plugin, args)
}
