.PHONY: gomods
gomods: # Install gomods
	go install github.com/jmank88/gomods@v0.1.5

.PHONY: modgraph
modgraph: gomods # Generate go.md module graph
	go install github.com/jmank88/modgraph@v0.1.0
	./modgraph > go.md
