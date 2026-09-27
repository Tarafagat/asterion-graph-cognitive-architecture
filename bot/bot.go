// Package bot implementa la idea del paper § 16: "un bot no debe ser una
// inteligencia separada por defecto — debe ser una interfaz hacia una
// inteligencia AGCA existente." Bot acá es deliberadamente delgado: envuelve
// un *runtime.Runtime ya construido y su propio agcaspec.BotDecl (nombre,
// interfaz declarada, permissions), y expone Ask como la única operación —
// nunca duplica memoria ni grafo por su cuenta.
package bot

import (
	"context"
	"fmt"

	"github.com/Tarafagat/asterion-language/agcaspec"

	"github.com/Tarafagat/asterion-graph-cognitive-architecture/runtime"
)

// defaultNeuronCapability es la capacidad de NEURONA que este Bot pide en
// cada Ask — ver el doc comment de runtime.RunCognitiveCycle sobre por
// qué esto es fijo en vez de derivado del goal: no hay Executive Agent
// real todavía (Milestone 6 del roadmap) que infiera capabilities desde
// lenguaje natural, y este Bot no finge tener uno.
const defaultNeuronCapability = "classification"

// Bot es una interfaz nombrada hacia una Intelligence ya construida.
type Bot struct {
	Decl    agcaspec.BotDecl
	Runtime *runtime.Runtime
}

// New envuelve un Runtime y el BotDecl que lo referencia — falla si el
// BotDecl no pertenece a la Intelligence de ese Runtime (un bug de uso,
// no algo que debería poder pasar en silencio).
func New(rt *runtime.Runtime, decl agcaspec.BotDecl) (*Bot, error) {
	if decl.Intelligence != rt.Intelligence.VarName {
		return nil, fmt.Errorf("bot: %q pertenece a la intelligence %q, no a %q", decl.Name, decl.Intelligence, rt.Intelligence.VarName)
	}
	return &Bot{Decl: decl, Runtime: rt}, nil
}

// Ask corre un ciclo cognitivo completo (runtime.RunCognitiveCycle) sobre
// el goal recibido y devuelve el resultado — la interfaz entera de un Bot
// en este MVP. Interface (terminal/web/api) es solo metadata acá: quién
// llama a Ask (una REPL de terminal, un handler HTTP) es responsabilidad
// del caller, no de este paquete.
func (b *Bot) Ask(ctx context.Context, goal string) (*runtime.CycleResult, error) {
	return b.Runtime.RunCognitiveCycle(ctx, goal, defaultNeuronCapability)
}
