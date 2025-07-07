package protos

import "github.com/smartcontractkit/chainlink-common/pkg/values/installer/pkg"

type ProtocGen struct{}

func (p ProtocGen) GenerateMany(fullPkg string, dirToConfig map[string]*pkg.CapabilityConfig) error {
	return p.run(fullPkg, func(gen *pkg.ProtocGen) error { return gen.GenerateMany(dirToConfig) })
}

func (p ProtocGen) Generate(fullPkg string, config *pkg.CapabilityConfig) error {
	return p.run(fullPkg, func(gen *pkg.ProtocGen) error { return gen.Generate(config) })
}

func (p ProtocGen) run(fullPkg string, fn func(gen *pkg.ProtocGen) error) error {
	if err := pkg.InstallProtocGenToDir("github.com/smartcontractkit/capabilities/bins/protoc-gen-capabilities", "github.com/smartcontractkit/capabilities/libs"); err != nil {
		return err
	}
	gen := &pkg.ProtocGen{ProtocHelper: ProtocHelper{fullPkg: fullPkg}, Plugins: []pkg.Plugin{{Name: "capabilities", Path: ".tools"}}}
	return fn(gen)
}
