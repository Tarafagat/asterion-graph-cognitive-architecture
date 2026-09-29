# Asterion Graph Cognitive Architecture (AGCA)

Implementación inicial de la propuesta de investigación ["Camino a la
AGI: Asterion Graph Cognitive
Architecture"](../asterion-language/docs/TUTORIAL.md) — una arquitectura
cognitiva donde un Cognitive Graph, neuronas intercambiables, agentes
especializados, memoria/experiencia y el sistema de Plugins de Asterion
cooperan como una sola inteligencia, declarada desde un `.asterion`.

## Los dos principios de AGCA

### Primer Principio — Representación

> **Un nodo puede describir una característica completa. Múltiples nodos
> describen una estructura. Múltiples estructuras describen un objeto.
> Múltiples objetos y relaciones describen un mundo. Miles de pequeñas
> inteligencias operan sobre ese mundo.**

### Segundo Principio de AGI — Experiencia

> **AGCA aprende cuando los resultados de sus interacciones con un World
> modifican la certeza con la que seleccionará capacidades frente a
> estados futuros semejantes del World.**

Una Experience no es memoria histórica ni un log de mensajes: es
**evidencia adquirida mediante interacción**. Conecta percepción →
decisión → acción → efecto → evaluación → aprendizaje, y su propósito es
que AGCA modifique gradualmente su comportamiento **sin destruir la
trazabilidad de sus decisiones**. Después de cada acto, AGCA puede
responder:

| Pregunta | Dónde vive la respuesta |
|---|---|
| ¿Qué ejecutaste? | `Decision.SelectedCapability` |
| ¿Por qué lo ejecutaste? | `Decision.CandidateCapabilities` (scores) + `Decision.ReasoningTrace` |
| ¿Qué contrato lo autorizó? | `Decision.ContractRef` + `tool.Contract` |
| ¿Qué esperabas que ocurriera? | `Contract.Guarantees` + `Decision.Confidence` |
| ¿Qué ocurrió realmente? | `Experience.ExecutionResult` + `WorldAfterRef` |
| ¿Qué aprendiste de ello? | `Experience.Evaluation` (4 fuentes independientes) |
| ¿Cómo cambió tu certeza? | `Experience.ConfidenceDelta` |

`asterion graph explain <decision-id>` imprime exactamente eso.

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
| **Tool contracts + frontera de ejecución** (`tool/`) | Real, y es la garantía de seguridad central: una Tool es un conjunto CERRADO de capabilities declaradas con `Tool.capability(...)`. `Registry.Resolve` es el ÚNICO camino a ejecución y falla con `ErrUnknownCapability` para cualquier ID no registrado — sin fuzzy matching ni fallback. No existe ninguna función que acepte código/comando/SQL y lo ejecute: `statistics.raw_sql` simplemente no existe salvo que alguien lo declare Y ate un handler. Declarar ≠ implementar: un contrato sin handler es visible pero devuelve `ErrNotImplemented`. |
| **Decision + trazabilidad** (`cognition/`) | Real. Cada acto produce una `Decision` con todos los candidatos, el desglose de score por componente, los descartados CON su motivo, las políticas que decidieron, la certeza previa y una traza legible. Se persiste SIEMPRE, incluso cuando no se ejecuta nada. |
| **Experience + aprendizaje de certeza** (`cognition/`) | Real. Tras ejecutar, 4 evaluadores independientes (tool/contrato/goal/world) producen un `FinalScore`; un `EMALearner` lo convierte en un delta de certeza para ESE `(capability, contexto)`. Verificado en vivo: 3 corridas exitosas llevan la certeza de 0.500 → 0.625 → 0.713 → 0.774 y el score de la decisión sube con ella. |
| **Roles con herencia** (`runtime/roles.go`) | Real. `AGCA.role(...)` declara la autoridad de QUIÉN OPERA (distinta del agente, que es quién actúa), con `inherits` para componer roles y `users=[...]` para resolver el rol de una identidad concreta. Herencia aplanada con detección de ciclos; **deny gana siempre** sobre cualquier allow heredado. La autoridad efectiva de un acto es la INTERSECCIÓN rol ∩ agente — ninguno puede ampliar al otro. Verificado en vivo: el mismo goal con `--user lectura@` (viewer) se rechaza nombrando al rol, y con `--user analista@` se selecciona. `asterion graph roles` imprime el conjunto efectivo por rol. |
| **Pipeline de enforcement** (`runtime.Act`) | Real, en orden obligatorio: Decision → Policy → Permiso del agente → Contrato → Requirements → Handler → Evaluación → Experience → Confidence. Permisos y políticas se verifican DOS veces (al seleccionar y antes de resolver el handler, porque una Decision puede persistirse y la autoridad se valida contra el estado de ahora). |
| **Persistencia cognitiva** (`cognition.FileStore`) | Real. Decisiones, experiencias y certeza aprendida sobreviven entre corridas del CLI en `~/.config/asterion/agca/<intelligence>/`. Sin esto el Segundo Principio no podría cumplirse: cada invocación sería amnésica. |
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

## Tools: capabilities declaradas, nunca ejecución arbitraria

Una Tool agrupa capabilities; cada capability tiene contrato explícito.
AGCA solo puede elegir entre ellas — no hay camino hacia `eval`, `exec`,
shell, SQL crudo ni un endpoint arbitrario:

```python
stats = Tool.define(name="Statistics", category="statistics")

search = Tool.capability(
    tool=stats,
    name="search_series",
    input=["query:String"],
    output=["series:Dataset"],
    effects=["read_only"],              # consultado ANTES de ejecutar
    requires=["network.available"],     # precondición verificada antes del handler
    guarantees=["returns:series"],      # contra esto se evalúa la Experience
)

# "Tool instalada ≠ Tool accesible": Inventory declara 2 capabilities,
# este agente solo puede usar una.
analyst = AGCA.agent(
    intelligence=brain,
    name="Analyst",
    allow=["statistics.search_series", "inventory.get_stock"],
    deny=["inventory.delete_record", "database.raw_sql", "system.shell"],
)

# Política del sistema: nada destructivo, sin importar quién lo pida.
AGCA.policy(intelligence=brain, name="ReadOnlyDataAccess", deny="destructive")

# Roles: la autoridad de quién OPERA (el agente es quién ACTÚA).
viewer = AGCA.role(intelligence=brain, name="viewer",
    allow=["statistics.search_series"], users=["lectura@empresa.com"])
operator = AGCA.role(intelligence=brain, name="operator",
    inherits=[viewer], allow=["inventory.delete_record"],
    deny=["statistics.correlation"], users=["operaciones@empresa.com"])
```

La autoridad con la que se ejecuta algo es la **intersección** de rol y
agente, y `deny` gana siempre — heredar amplía lo permitido, nunca
reabre lo prohibido.

Una Tool de base de datos puede usar SQL **por dentro** — pero AGCA solo
conoce `inventory.get_stock`, que resuelve a un handler predefinido.
`AGCA → "DROP TABLE ..."` no es posible: ese string no es un
CapabilityID y no hay ninguna API que lo acepte.

**Declarar no es implementar**: un `Tool.capability(...)` del `.asterion`
queda visible y puntuable, pero solo se ejecuta si un programa Go le ata
un handler con `runtime.BindHandler` — hasta entonces devuelve
`ErrNotImplemented`, nunca una respuesta improvisada.

## Usarlo (vía `asterion-core`)

```bash
asterion graph validate ./company.asterion
asterion graph inspect ./company.asterion --json
asterion graph run ./company.asterion --goal "necesito consultar el inventario" --capability classification
asterion graph bot run ./company.asterion --bot admin_bot

# Capa de Experience (Segundo Principio):
asterion graph act ./company.asterion --goal "buscar la serie histórica" --intent search_series --agent analyst
asterion graph decisions ./company.asterion
asterion graph explain ./company.asterion <decision-id>
asterion graph experience ./company.asterion [experience-id]
asterion graph roles ./company.asterion                 # qué puede cada rol (herencia resuelta)
asterion graph act ./company.asterion --goal "..." --user analista@empresa.com
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
| 8 | Event Ingestion + ExperienceRecord | **Parcial** | `cognition.Experience` es completa: referencia su Decision, el World antes/después, la evaluación de 4 fuentes, y el delta de certeza — persistida en disco. Event Ingestion (que un evento de un Plugin se convierta en un Observation Node — § 8 del paper) sigue sin existir: hoy la única "observación" es el goal que entra a `Act`/`RunCognitiveCycle`. |
| 9 | GGUFNeuron | **No empezado** | Un `AGCA.neuron(runtime="gguf", ...)` se compila, se registra y se lista con `Healthy: false` (honesto: sin backend real) — invocarlo da `neuron.ErrNotImplemented`, nunca una respuesta simulada. |
| 10 | RemoteLLMNeuron genérico | **No empezado** | Mismo tratamiento que GGUFNeuron: `AGCA.neuron(adapter="remote-llm", ...)` se registra `Healthy: false`. |
| 11 | Cognitive Router + Policy Engine | **Parcial** | Cognitive Router: REAL para capabilities de Tool — `cognition.Selector` puntúa con la fórmula del § 16 (goal + world + contrato + experiencia − penalidad de política) y se abstiene bajo el umbral. Policy Engine: REAL para lo que un contrato declara — `AGCA.policy(deny="destructive")` veta por effects antes de ejecutar, y queda registrado en la Decision. Falta: el router de NEURONAS sigue con la heurística vieja (local antes que remota), y el lenguaje de políticas solo entiende efectos, no condiciones arbitrarias (`when="data.sensitivity>=confidential"` se compila pero no se evalúa). |
| 12 | Bots/interfaces y una inteligencia compartida | **Hecho** | `bot.Bot` + `asterion graph bot run` — una REPL de terminal real, verificada en vivo, que comparte el mismo `Runtime` (grafo + memoria + neuronas) que `graph run` usa directamente — nunca una inteligencia duplicada. |
| 13 | `asterion talk`, patches, validate/plan, approval gates | **No empezado** | No existe ninguna ruta conversacional que proponga cambios al propio `.asterion` — eso es una capa de generación de código sobre el compilador, no construida. |
| 14 | Asterion Secrets references + aislamiento de credenciales | **Parcial** | `tool.RequireIsolation` exige que una capability de una Tool con `isolation` declarada tenga su handler confirmado como aislado (`BindHandler(..., isolated=true)`) — sin eso no se ejecuta, nunca hereda los permisos del proceso host. `AGCA.secret(...)` es una REFERENCIA real, nunca un valor (verificado: ningún test ni ejemplo pasa un secreto real por el DSL) — `source` literal para un secreto que no pertenece a ningún plugin importado, o `from`/`field` para declarar con anticipación uno de un plugin todavía no resoluble localmente. Más allá de eso, cualquier campo `secret: true` de un plugin YA resoluble se DESCUBRE SOLO (ver Milestone 7) — sin declarar nada. Lo que falta: conectar cualquiera de las dos referencias con el mecanismo real de secretos de `asterion-core` (inyectarla de verdad al proceso de un plugin autorizado en runtime) — hoy son solo datos que el compilador/runtime validan y reportan, sin un lado de ejecución. |
| 15 | Vision example + World Model mínimo | **No empezado** | Sin ejemplo de visión geométrica (Curves/Shapes/Objects) ni ninguna forma de simulación de futuros hipotéticos antes de actuar (§ 3 del paper). |
| 16 | Observabilidad, benchmark, documentación, portabilidad end-to-end, hardening | **Parcial** | Documentación: extensa (este README, `spec/grammar.md`, `docs/TUTORIAL.md` § 8, todo con salida real capturada en vivo). Observabilidad: cada ciclo cognitivo genera y propaga un `TraceID` real a través de `Provenance` — sin métricas ni un exportador (Prometheus, OpenTelemetry). Sin benchmarks, sin hardening de seguridad más allá de "nunca fabricar una respuesta"/"secretos nunca en el DSL como valor". |

**Resumen**: 3 de 16 milestones completos (`2`, `5`, `12`), 9 parciales
con el detalle exacto de qué falta en cada uno, 4 sin empezar. Ningún
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
tool/         Contratos de capability + Registry + frontera contra ejecución arbitraria
cognition/    Vector, Decision, Experience, evaluación multi-fuente, certeza por contexto, persistencia
```

El ejemplo real (con un bot) vive en
`asterion-language/examples/agca-company.asterion` — este repo no
duplica archivos `.asterion` de ejemplo, para no tener dos copias
desincronizadas del mismo archivo.

## Licencia

Apache License 2.0 — igual que el resto del ecosistema Asterion.
