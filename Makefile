.PHONY: modgraph
modgraph:
	go install github.com/jmank88/gomods@v0.1.5
	go install github.com/jmank88/modgraph@8b0e2b07928b34a6e9d67639061ecd712ea8ee89
	./modgraph > go.md
