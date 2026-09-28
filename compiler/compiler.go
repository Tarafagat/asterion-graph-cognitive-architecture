// Package compiler es el puente entre un archivo .asterion y este
// runtime: parsea + compila vía asterion-language (parser + agcaspec),
// nunca reimplementa lexer/parser acá — mismo criterio que
// asterion-core/cmd/asterion/plugin_system.go con systemspec.
package compiler

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Tarafagat/asterion-language/agcaspec"
	langparser "github.com/Tarafagat/asterion-language/parser"
)

// CompileFile lee, parsea y compila un archivo .asterion a un
// *agcaspec.Spec. Cualquier error (de lectura, de sintaxis o de
// compilación AGCA) se devuelve como un solo error con los diagnósticos
// completos adentro — nunca un Spec parcial: si el archivo no compila
// del todo, no hay nada seguro que construir con él.
func CompileFile(path string) (*agcaspec.Spec, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no pude leer %s: %w", path, err)
	}

	prog, parseDiags := langparser.Parse(src, path)
	if parseDiags.HasErrors() {
		return nil, fmt.Errorf("%s no compila:\n%s", path, parseDiags.String())
	}

	// baseDir: contra él se resuelve un Import(path=...) relativo dentro
	// del archivo (ej. `Import(path="./sistema.asterion")` apunta al
	// lado del propio archivo, no al cwd de quien corre 'asterion graph').
	spec, compileDiags := agcaspec.Compile(prog, filepath.Dir(path))
	if compileDiags.HasErrors() {
		return nil, fmt.Errorf("%s no se pudo compilar a una inteligencia AGCA:\n%s", path, compileDiags.String())
	}
	return spec, nil
}
