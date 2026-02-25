.PHONY: modgraph
modgraph:
	go install github.com/jmank88/gomods@v0.1.6
	go install github.com/jmank88/modgraph@v0.1.1
	./modgraph > go.md

.PHONY: gomods
gomods: ## Install gomods
	go install github.com/jmank88/gomods@v0.1.6

.PHONY: gomodtidy
gomodtidy: gomods ## Run go mod tidy on all modules.
	gomods tidy

.PHONY: tidy
tidy: gomodtidy gomods ## Tidy all modules and add to git.
	git add '**go.*'
