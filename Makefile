.PHONY: modgraph
modgraph:
	go install github.com/jmank88/gomods@v0.1.6
	go install github.com/jmank88/modgraph@v0.1.1
	./modgraph > go.md
