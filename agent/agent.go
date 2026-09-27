// Package agent implementa Agent/Swarm/Executive y el Scheduler
// propuestos en el paper § 6 y § 21.2: "la arquitectura puede activar
// decenas, cientos o miles de agentes, pero no necesita mantenerlos todos
// ejecutándose simultáneamente — el scheduler asigna presupuesto de CPU,
// GPU, memoria, tokens, latencia y costo." Este MVP acota "presupuesto" a
// lo único que un runtime en Go puede limitar de forma honesta sin
// integrar un sistema de cuotas externo: concurrencia (goroutines
// simultáneas) — ver Scheduler.MaxConcurrency.
package agent

import (
	"context"
	"sync"
)

// Task es una unidad de trabajo que un swarm procesa — Input llega a la
// función que Scheduler.RunSwarm recibe, Output/Err quedan en el
// resultado en el mismo índice que el input, para que el caller pueda
// reconstruir qué resultado corresponde a qué tarea.
type Task struct {
	ID    string
	Input string
}

// Result es el resultado de procesar una Task.
type Result struct {
	TaskID string
	Output string
	Err    error
}

// Scheduler activa agentes "bajo demanda" en vez de mantener procesos
// permanentes por instancia (§ 6.1 del paper, y el checklist del Apéndice
// A: "Swarms usan scheduler y pools, no procesos permanentes por
// instancia"). MaxConcurrency es el presupuesto real que este MVP
// impone: cuántas Tasks de un mismo swarm pueden correr al mismo tiempo.
type Scheduler struct {
	MaxConcurrency int
}

// NewScheduler crea un Scheduler. maxConcurrency <= 0 se trata como 1
// (nunca 0 ni negativo — un scheduler que no puede correr nada no es un
// scheduler, es un bug de configuración disfrazado).
func NewScheduler(maxConcurrency int) *Scheduler {
	if maxConcurrency <= 0 {
		maxConcurrency = 1
	}
	return &Scheduler{MaxConcurrency: maxConcurrency}
}

// RunSwarm activa work(task) para cada Task, con hasta MaxConcurrency
// ejecuciones simultáneas — real concurrencia con goroutines + un
// semáforo por canal con buffer, no una simulación secuencial disfrazada
// de paralela. Devuelve los resultados en el MISMO ORDEN que las tasks de
// entrada (no en orden de finalización), para que el caller pueda
// correlacionarlos posicionalmente sin tener que indexar por TaskID.
func (s *Scheduler) RunSwarm(ctx context.Context, tasks []Task, work func(context.Context, Task) Result) []Result {
	results := make([]Result, len(tasks))
	sem := make(chan struct{}, s.MaxConcurrency)
	var wg sync.WaitGroup

	for i, task := range tasks {
		wg.Add(1)
		go func(i int, task Task) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = work(ctx, task)
		}(i, task)
	}
	wg.Wait()
	return results
}
