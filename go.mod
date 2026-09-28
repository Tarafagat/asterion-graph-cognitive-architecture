module github.com/Tarafagat/asterion-graph-cognitive-architecture

go 1.25.0

// asterion-language todavía no está publicado en ningún registry — hasta
// que lo esté, se necesita clonado como carpeta hermana (mismo criterio
// que asterion-core/asterion-lab con asterion-plugin-contract). Este
// repo compila un .asterion vía asterion-language/parser +
// asterion-language/agcaspec — nunca reimplementa el lexer/parser acá.
replace github.com/Tarafagat/asterion-language => ../asterion-language

replace github.com/Tarafagat/asterion-plugin-contract => ../asterion-plugin-contract

require (
	github.com/Tarafagat/asterion-language v0.0.0-00010101000000-000000000000
	github.com/Tarafagat/asterion-plugin-contract v0.0.0-00010101000000-000000000000
)

require gopkg.in/yaml.v3 v3.0.1 // indirect
