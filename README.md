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

Esto es un subconjunto de los 16 milestones del roadmap del paper, no
los 16 completos — **deliberadamente**, siguiendo la propia instrucción
del documento: *"No intentes construir todas las capas a la vez."* Ver
"Roadmap completo" más abajo para el estado milestone por milestone.

| Subsistema | Estado |
|---|---|
| **Cognitive Graph** (`graph/`) | Real. Nodos/edges tipados, confianza, procedencia, versión por escritura, snapshots, consultas por Kind. En memoria (no persiste a disco todavía — ver "Roadmap completo"). |
| **Neuron Contract + Registry** (`neuron/`) | Real. `Neuron` es una interfaz model-agnostic; `Registry.Match` filtra por capability + salud, locales antes que remotas. **Una** neurona con backend real: `DeterministicNeuron` (clasificación por keywords, 100% reproducible). GGUF/remote-llm son manifiestos válidos (se compilan, se registran, se listan) pero sin backend — invocarlas da `ErrNotImplemented`, nunca una respuesta inventada. |
| **Agent Scheduler** (`agent/`) | Real. `Scheduler.RunSwarm` corre tareas con concurrencia acotada (goroutines + semáforo), nunca un proceso permanente por instancia. Sin Executive Agent real todavía (ver "Roadmap completo"). |
| **Capability Registry** (`capability/`) | Real como registro/matching en memoria, y con un caso real de descubrimiento automático: `capability.DeriveFromManifest` lee el `plugin.yaml` REAL de un plugin (`resources[].crud` + `actions[]`) y arma sus capabilities sin inventar nada — `runtime.Build` lo usa para poblar el Registry solo con los plugins que un `Import(...)` trajo con route LOCAL (ver `runtime.discoverImportedPlugins`). Lo que sigue faltando: un plugin de route git (sin clonar) o uno instalado directo en `asterion-core` sin pasar por `Import(...)`. |
| **Experience Store** (`experience/`) | Real. Acumula un `Record` por ciclo cognitivo (goal, neurona usada, resultado, error, duración). Nadie lee estos datos para influir routing todavía — ver "Roadmap completo". |
| **Runtime / ciclo cognitivo** (`runtime/`) | Real. `Build` arma una Intelligence completa desde un `agcaspec.Spec`; `RunCognitiveCycle` corre perceive → match de neurona → merge en el grafo → experience, siguiendo el pseudocódigo del Apéndice C del paper — acotado a UNA capability de neurona explícita (sin planner que la infiera del goal). |
| **Bot** (`bot/`) | Real pero mínimo: una interfaz nombrada hacia una Intelligence (§ 16 del paper: "nunca una inteligencia separada por defecto"). `asterion graph bot run` es una REPL de terminal real. |
| **Compilador `.asterion` → Spec** (`compiler/`) | Real. Parsea + compila vía `asterion-language/parser` + `asterion-language/agcaspec` — nunca reimplementa lexer/parser acá. |
| **`Import(...)`: unión con `System.*`** (`asterion-language/agcaspec`) | Real. Un archivo AGCA puede `Import(path="...")` un archivo de sistema de plugins (`System.plugin(...)`, ver `asterion-language/systemspec`) y referenciar sus plugins como `<var>.<plugin>`. No copia el wiring (`System.wire(...)`) del archivo importado, ni trae declaraciones `AGCA.*` de otro archivo (sin import anidado todavía). |
| **Descubrimiento automático de capabilities/secretos** (`runtime.discoverImportedPlugins`) | Real. Para cada plugin que un `Import(...)` trajo con route LOCAL, lee su `plugin.yaml` de verdad UNA vez y deriva sus capabilities (arriba) Y qué campos de su `config_schema` son `secret: true` — sin que el archivo AGCA tenga que declarar un `AGCA.secret(from=, field=)` manual solo para restatear información que el plugin ya declaró. Una route de git sin clonar, o un `plugin.yaml` inválido, quedan honestamente marcados "no resuelto" (con el motivo) — nunca silenciados. |

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

# Import(...) UNE este archivo con un sistema de plugins YA declarado en
# otro (ver asterion-language/examples/tutorial-system.asterion) — trae
# sys.db/sys.api para referenciarlos, sin copiar su contenido a mano.
sys = Import(path="./tutorial-system.asterion")

brain = AGCA.intelligence(name="CompanyBrain")
world = AGCA.graph(intelligence=brain, name="CompanyWorld", hierarchical=true, persistent=true)

fast = AGCA.neuron(intelligence=brain, name="LocalFast", runtime="gguf", capabilities=["classification"], privacy="local")

ops = AGCA.swarm(intelligence=brain, name="Operations", instances="adaptive")
executive = AGCA.agent(intelligence=brain, name="Executive", strategy="adaptive")

AGCA.requires_capability(intelligence=brain, capability="database.query")

memory = AGCA.memory(intelligence=brain, name="LongTerm", type="hybrid", graph=world)

admin_bot = AGCA.bot(intelligence=brain, name="AdminAssistant", interface="terminal", permissions=["inventory.read"])
```

Con `sys.db` resoluble localmente (una carpeta que ya existe en disco),
`asterion graph inspect` YA descubre sus capabilities y qué campos de su
config son secretos, sin que este archivo tenga que declarar nada más —
ver "Estado real" arriba. `AGCA.secret(from=, field=)` sigue existiendo
para el caso de borde en que el plugin todavía NO es resoluble
localmente (route de git sin clonar) — usarlo para repetir un secreto de
un plugin ya resoluble sería una doble declaración sin información
nueva (ver `spec/grammar.md`).

Ver `asterion-language/spec/grammar.md` § "DSL de inteligencia cognitiva
(AGCA.\*)" por la gramática/tabla de verbos completa, los códigos
`ASTR600`-`ASTR613`, y `asterion-language/examples/agca-company.asterion`
por un ejemplo real y completo (incluye un bot y un `Import(...)`).

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
- **Nunca declarar a mano lo que un plugin ya declaró.** Si un
  `Import(...)` puede leer el `plugin.yaml` real de un plugin (route
  local), sus capabilities y sus campos `secret: true` se descubren
  solos — pedirle al archivo AGCA que los repita con
  `AGCA.secret(from=, field=)` sería una doble declaración sin ninguna
  información nueva. Esa forma explícita queda reservada al único caso
  donde SÍ hace falta: un plugin todavía no resoluble localmente (route
  de git sin clonar).

## Roadmap completo (los 16 milestones del § 28 del paper)

El paper ("Camino a la AGI") define su propio orden de implementación
recomendado en 16 milestones (§ 28) — la tabla siguiente es el estado
REAL de cada uno contra este repo hoy, no una intención. `hecho` exige
código real y probado (tests, o verificado en vivo); `parcial` significa
que una parte concreta existe y otra parte concreta falta, listada
explícitamente; `no empezado` significa exactamente eso, nunca simulado
por otro lado.

| # | Milestone (según el paper) | Estado | Detalle |
|---|---|---|---|
| 1 | Especificaciones + IR + Node/Edge/Subgraph | **Parcial** | `agcaspec.Spec` es el IR; `graph.Node`/`graph.Edge`/`graph.Store` son reales y probados. Sin un tipo `Subgraph` dedicado para expandir Graph-of-Graphs jerárquico (§ 4.3 del paper) — hoy la única forma de "consultar un patrón" es `Store.ByKind`/`Store.EdgesFrom`. |
| 2 | GraphStore + snapshots + provenance | **Hecho** | `graph.Store`: nodos/edges tipados, confianza, procedencia (`Provenance{Source, TraceID}`), versión por escritura, `Snapshot()` congelado — todo con tests (`graph/graph_test.go`), incluido uno que confirma que un snapshot no cambia tras escrituras posteriores. Solo en memoria — no persiste a disco entre corridas. |
| 3 | System IR + Local Environment Resolver + `asterion up` | **Parcial** | El "System IR" YA EXISTE — es `systemspec` (construido esta misma sesión, no como parte de AGCA) — y `Import(path=...)` en `agcaspec` es la unión real entre ambos DSL. No existe un `asterion up` unificado: `asterion plugin system apply` (para el sistema de plugins) y `asterion graph run`/`bot run` (para la Intelligence) siguen siendo comandos separados. |
| 4 | Target Resolver local/attach/provider + plan portable | **No empezado** (para AGCA) | Existe una versión LIMITADA para infraestructura cruda (`asterion language apply`, solo `Provider.gcp.instance(...)`) — sin relación con AGCA. Ningún concepto de "target" existe para una Intelligence. |
| 5 | Neuron Contract + DeterministicNeuron | **Hecho** | `neuron.Neuron` (interfaz model-agnostic) + `neuron.DeterministicNeuron` (clasificación real por keywords, 100% reproducible, con tests). |
| 6 | Agent Runtime + Executive + scheduler | **Parcial** | `agent.Scheduler.RunSwarm` es real (goroutines + semáforo, concurrencia acotada, probado con `-race`). `AGCA.swarm`/`AGCA.agent` se declaran, compilan y listan (`graph inspect`) — pero `RunCognitiveCycle` no los invoca: no hay today un Executive Agent tomando decisiones reales, el ciclo va directo a `Neurons.Match`. |
| 7 | Capability Registry conectado al sistema de Plugins | **Parcial** | `capability.DeriveFromManifest` deriva capabilities REALES del `plugin.yaml` de un plugin (`resources[].crud`+`actions[]`) — `runtime.Build` las registra automáticamente para todo plugin que un `Import(...)` trajo con route LOCAL (verificado en vivo: `asterion graph inspect` muestra "Capabilities descubiertas"). Sigue faltando: un plugin de route git sin clonar, y cualquier plugin ya instalado/corriendo en `asterion-core` que no haya pasado por un `Import(...)` — ningún descubrimiento contra el estado real de esa máquina todavía. |
| 8 | Event Ingestion + ExperienceRecord | **Parcial** | `experience.Record`/`experience.Store`: reales, un registro real por ciclo cognitivo (éxito o error). Event Ingestion (que un evento de un Plugin se convierta en un Observation Node — § 8 del paper) no existe: hoy la única "observación" es el `goal` de texto que entra a `RunCognitiveCycle`. |
| 9 | GGUFNeuron | **No empezado** | Un `AGCA.neuron(runtime="gguf", ...)` se compila, se registra y se lista con `Healthy: false` (honesto: sin backend real) — invocarlo da `neuron.ErrNotImplemented`, nunca una respuesta simulada. |
| 10 | RemoteLLMNeuron genérico | **No empezado** | Mismo tratamiento que GGUFNeuron: `AGCA.neuron(adapter="remote-llm", ...)` se registra `Healthy: false`. |
| 11 | Cognitive Router + Policy Engine | **Parcial** | Cognitive Router: `neuron.Registry.Match` filtra por capability + salud y prefiere locales sobre remotas — una heurística fija, documentada como tal, no la fórmula de score ponderado del § 11 del paper (reputación/costo/latencia/privacidad). Policy Engine: `AGCA.policy(...)` se compila y se expone como datos (`when`/`allow`/`deny` en texto plano) — nada lo EVALÚA en runtime todavía. |
| 12 | Bots/interfaces y una inteligencia compartida | **Hecho** | `bot.Bot` + `asterion graph bot run` — una REPL de terminal real, verificada en vivo, que comparte el mismo `Runtime` (grafo + memoria + neuronas) que `graph run` usa directamente — nunca una inteligencia duplicada. |
| 13 | `asterion talk`, patches, validate/plan, approval gates | **No empezado** | No existe ninguna ruta conversacional que proponga cambios al propio `.asterion` — eso es una capa de generación de código sobre el compilador, no construida. |
| 14 | Asterion Secrets references + aislamiento de credenciales | **Parcial** | `AGCA.secret(...)` es una REFERENCIA real, nunca un valor (verificado: ningún test ni ejemplo pasa un secreto real por el DSL) — `source` literal para un secreto que no pertenece a ningún plugin importado, o `from`/`field` para declarar con anticipación uno de un plugin todavía no resoluble localmente. Más allá de eso, cualquier campo `secret: true` de un plugin YA resoluble se DESCUBRE SOLO (ver Milestone 7) — sin declarar nada. Lo que falta: conectar cualquiera de las dos referencias con el mecanismo real de secretos de `asterion-core` (inyectarla de verdad al proceso de un plugin autorizado en runtime) — hoy son solo datos que el compilador/runtime validan y reportan, sin un lado de ejecución. |
| 15 | Vision example + World Model mínimo | **No empezado** | Sin ejemplo de visión geométrica (Curves/Shapes/Objects) ni ninguna forma de simulación de futuros hipotéticos antes de actuar (§ 3 del paper). |
| 16 | Observabilidad, benchmark, documentación, portabilidad end-to-end, hardening | **Parcial** | Documentación: extensa (este README, `spec/grammar.md`, `docs/TUTORIAL.md` § 8, todo con salida real capturada en vivo). Observabilidad: cada ciclo cognitivo genera y propaga un `TraceID` real a través de `Provenance` — sin métricas ni un exportador (Prometheus, OpenTelemetry). Sin benchmarks, sin hardening de seguridad más allá de "nunca fabricar una respuesta"/"secretos nunca en el DSL como valor". |

**Resumen**: 3 de 16 milestones completos (`2`, `5`, `12`), 8 parciales
con el detalle exacto de qué falta en cada uno, 5 sin empezar. Ningún
casillero de esta tabla está "hecho" por optimismo — cada uno que dice
`Hecho` tiene un test o una verificación en vivo detrás en este mismo
repo.

## Estructura

```
graph/        Node/Edge/Store — Cognitive Graph en memoria
neuron/       Neuron contract, Registry, DeterministicNeuron
agent/        Scheduler (swarms con concurrencia acotada)
capability/   Registro de capabilities de plugin + DeriveFromManifest (deriva capabilities reales de un plugin.yaml)
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
