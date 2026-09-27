# Asterion Graph Cognitive Architecture (AGCA)

Implementación inicial de la propuesta de investigación ["Camino a la
AGI: Asterion Graph Cognitive
Architecture"](../asterion-language/docs/TUTORIAL.md) — una arquitectura
cognitiva donde un Cognitive Graph, neuronas intercambiables, agentes
especializados, memoria/experiencia y el sistema de Plugins de Asterion
cooperan como una sola inteligencia, declarada desde un `.asterion`.

**Este repo es una librería Go, no un CLI standalone.** El CLI vive en
`asterion-core` (`asterion graph validate|inspect|run|bot run` —
`asterion-core/cmd/asterion/graph.go`), que importa los paquetes de acá
igual que ya importa `asterion-language` (`replace` en `go.mod`, mismo
criterio que `asterion-lab`/`asterion-plugin-contract`).

## Estado real, sin adornos

Esto es el MVP 1-3 aproximado del roadmap del paper (Fase 0-2), no las 16
fases completas — **deliberadamente**, siguiendo la propia instrucción
del documento: *"No intentes construir todas las capas a la vez."*

| Subsistema | Estado |
|---|---|
| **Cognitive Graph** (`graph/`) | Real. Nodos/edges tipados, confianza, procedencia, versión por escritura, snapshots, consultas por Kind. En memoria (no persiste a disco todavía — ver "Qué falta"). |
| **Neuron Contract + Registry** (`neuron/`) | Real. `Neuron` es una interfaz model-agnostic; `Registry.Match` filtra por capability + salud, locales antes que remotas. **Una** neurona con backend real: `DeterministicNeuron` (clasificación por keywords, 100% reproducible). GGUF/remote-llm son manifiestos válidos (se compilan, se registran, se listan) pero sin backend — invocarlas da `ErrNotImplemented`, nunca una respuesta inventada. |
| **Agent Scheduler** (`agent/`) | Real. `Scheduler.RunSwarm` corre tareas con concurrencia acotada (goroutines + semáforo), nunca un proceso permanente por instancia. Sin Executive Agent real todavía (ver "Qué falta"). |
| **Capability Registry** (`capability/`) | Real como registro/matching en memoria. **No conectado a Asterion Plugins de verdad todavía** — `Register` es manual, no descubre plugins instalados en `asterion-core`. |
| **Experience Store** (`experience/`) | Real. Acumula un `Record` por ciclo cognitivo (goal, neurona usada, resultado, error, duración). Nadie lee estos datos para influir routing todavía — ver "Qué falta". |
| **Runtime / ciclo cognitivo** (`runtime/`) | Real. `Build` arma una Intelligence completa desde un `agcaspec.Spec`; `RunCognitiveCycle` corre perceive → match de neurona → merge en el grafo → experience, siguiendo el pseudocódigo del Apéndice C del paper — acotado a UNA capability de neurona explícita (sin planner que la infiera del goal). |
| **Bot** (`bot/`) | Real pero mínimo: una interfaz nombrada hacia una Intelligence (§ 16 del paper: "nunca una inteligencia separada por defecto"). `asterion graph bot run` es una REPL de terminal real. |
| **Compilador `.asterion` → Spec** (`compiler/`) | Real. Parsea + compila vía `asterion-language/parser` + `asterion-language/agcaspec` — nunca reimplementa lexer/parser acá. |

## El DSL: `AGCA.*`

El paper propone una sintaxis de bloques con llaves
(`intelligence X { graph Y { ... } }`) que Asterion Language no tiene
— este lenguaje no usa llaves (ver `asterion-language/spec/grammar.md` §
Léxico). `asterion-language/agcaspec` adapta la misma idea al estilo que
ya usan `Contract.*`/`System.*`: una secuencia de llamadas
`AGCA.<verbo>(clave=valor, ...)`, cada una asignada a un nombre de
variable que las demás referencian después.

```python
language "0.1"

brain = AGCA.intelligence(name="CompanyBrain")
world = AGCA.graph(intelligence=brain, name="CompanyWorld", hierarchical=true, persistent=true)

fast = AGCA.neuron(intelligence=brain, name="LocalFast", runtime="gguf", capabilities=["classification"], privacy="local")

ops = AGCA.swarm(intelligence=brain, name="Operations", instances="adaptive")
executive = AGCA.agent(intelligence=brain, name="Executive", strategy="adaptive")

AGCA.requires_capability(intelligence=brain, capability="database.query")

memory = AGCA.memory(intelligence=brain, name="LongTerm", type="hybrid", graph=world)

admin_bot = AGCA.bot(intelligence=brain, name="AdminAssistant", interface="terminal", permissions=["inventory.read"])

db_password = AGCA.secret(name="DatabasePassword", source="mycompany/prod/database_password")
```

Ver `asterion-language/spec/grammar.md` § "DSL de inteligencia cognitiva
(AGCA.\*)" por la gramática/tabla de verbos completa, los códigos
`ASTR600`-`ASTR608`, y `asterion-language/examples/agca-company.asterion`
por un ejemplo real y completo (incluye un bot).

## Usarlo (vía `asterion-core`)

```bash
asterion graph validate ./company.asterion
asterion graph inspect ./company.asterion --json
asterion graph run ./company.asterion --goal "necesito consultar el inventario" --capability classification
asterion graph bot run ./company.asterion --bot admin_bot
```

`inspect` construye la Intelligence completa (grafo + neurona registry +
swarms/agentes/capabilities/bots) y muestra qué neuronas están
`disponible` vs `fuera de servicio` — nunca esconde una neurona
declarada-pero-sin-backend, la lista igual con su estado real. `run`
corre un ciclo cognitivo puntual. `bot run` abre una sesión de terminal
donde cada línea es un goal nuevo.

## Diseño

- **Model-agnostic de verdad**: el runtime central (`runtime/`,
  `neuron/`) nunca importa un SDK de proveedor de LLM concreto — GPT,
  Claude, Gemini, Llama entrarían como implementaciones de la interfaz
  `neuron.Neuron`, nunca como dependencias estructurales (§ 5.3 del
  paper).
- **Nunca fabricar una respuesta**: una neurona sin backend real
  (`placeholderNeuron` en `runtime/runtime.go`) se registra con
  `Healthy: false` — el Neuron Router la excluye de cualquier ciclo real,
  en vez de simular una respuesta en su nombre. Mismo criterio que ya
  rige el resto del ecosistema Asterion (ver la disciplina de "todo lo
  documentado está corrido de verdad" en
  `asterion-language/docs/TUTORIAL.md`).
- **Concurrencia real, no simulada**: `agent.Scheduler.RunSwarm` usa
  goroutines + un semáforo de verdad — confirmado con `go test -race` y
  un test que mide la concurrencia máxima observada.
- **Presupuesto explícito, no procesos permanentes**: "128 agentes" en
  un `.asterion` nunca implica 128 goroutines vivas para siempre — el
  Scheduler las activa bajo demanda, acotadas por `MaxConcurrency`.

## Qué falta (a propósito, ver el roadmap del paper § 27)

- **Persistencia real del Cognitive Graph** (hoy: solo en memoria, se
  pierde al terminar el proceso) — Milestone 2 avanzado / Fase 1 tardía
  del roadmap.
- **Executive Agent real** que derive qué capability de neurona hace
  falta a partir de un goal en lenguaje natural — hoy `--capability` es
  siempre explícito. Milestone 6.
- **GGUFNeuron/RemoteLLMNeuron con backend real** (llama.cpp, un adapter
  HTTP genérico a un proveedor remoto) — Milestones 9-10. Hoy sus
  manifiestos se compilan y registran, pero `Transform` da
  `ErrNotImplemented`.
- **Capability Registry conectado a Asterion Plugins de verdad** (hoy:
  registro manual en memoria, no descubre plugins instalados en
  `asterion-core`) — Milestone 7.
- **Policy Engine que EVALÚE `AGCA.policy(...)`** (hoy: se compila y se
  expone como datos — `when`/`allow`/`deny` en texto plano — pero nada lo
  aplica todavía) — Milestone 11.
- **World Model / simulación** antes de actuar (§ 3 del paper) —
  Milestone 8+.
- **`routing.learn_from`**: el Experience Store acumula, pero nada lee
  esos datos para influir el próximo `Registry.Match` todavía — MVP 5 /
  Fase 6.
- **`asterion plugin system` como fuente de capabilities**: hoy
  `AGCA.requires_capability(...)` y `capability.Registry` son
  independientes de `System.plugin(...)`/`System.wire(...)` (ver
  `asterion-language/systemspec`) — conectar ambos (un sistema de
  plugins YA instalado satisfaciendo las capabilities que una
  Intelligence requiere) es trabajo futuro explícito, no simulado acá.

## Estructura

```
graph/        Node/Edge/Store — Cognitive Graph en memoria
neuron/       Neuron contract, Registry, DeterministicNeuron
agent/        Scheduler (swarms con concurrencia acotada)
capability/   Registro de capabilities de plugin (manual, no conectado a Asterion Plugins todavía)
experience/   ExperienceRecord + Store (acumula, nadie lo lee todavía)
compiler/     .asterion -> agcaspec.Spec (vía asterion-language)
runtime/      Arma una Intelligence completa + RunCognitiveCycle
bot/          Interfaz nombrada hacia una Intelligence ya construida
```

El ejemplo real (con un bot) vive en
`asterion-language/examples/agca-company.asterion` — este repo no
duplica archivos `.asterion` de ejemplo, para no tener dos copias
desincronizadas del mismo archivo.

## Licencia

Apache License 2.0 — igual que el resto del ecosistema Asterion.
