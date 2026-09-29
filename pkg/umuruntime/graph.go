package umuruntime

import "errors"

// Graph is a validated set of plugins in dependency order.
type Graph struct {
	// order is topological: every plugin comes after the providers of its
	// needs. Ties keep registration order, so the result is deterministic.
	order []Registered
	// providers lists, per capability, the plugins providing it in
	// registration order — the order the Host tries them in.
	providers map[Capability][]Registered
}

// Resolve validates regs and orders them. Rules
// (docs/UMU_RUNTIME_PLUGIN_PLAN.md §4.3):
//
//  1. plugin names are unique;
//  2. every hard need has at least one provider; a soft need may have none;
//  3. the consumer → provider relation is acyclic (a plugin needing a
//     capability it provides itself is not a cycle — it is simply ignored);
//  4. several providers of one capability are allowed and tried in
//     registration order.
func Resolve(regs []Registered) (*Graph, error) {
	g := &Graph{providers: map[Capability][]Registered{}}

	index := make(map[string]int, len(regs))
	for i, r := range regs {
		if r.Plugin == nil {
			return nil, errors.New("umuruntime: nil plugin registered")
		}
		name := r.Plugin.Name()
		if _, dup := index[name]; dup {
			return nil, &DuplicatePluginError{Name: name}
		}
		index[name] = i
		for _, c := range r.Plugin.Provides() {
			g.providers[c] = append(g.providers[c], r)
		}
	}

	// edges[i] = indexes of the plugins i depends on, in need order then
	// registration order.
	edges := make([][]int, len(regs))
	for i, r := range regs {
		seen := map[int]bool{}
		for _, n := range r.Plugin.Needs() {
			provs := g.providers[n.Cap]
			if len(provs) == 0 && !n.Soft {
				return nil, &MissingProviderError{Plugin: r.Plugin.Name(), Cap: n.Cap}
			}
			for _, p := range provs {
				j := index[p.Plugin.Name()]
				if j == i || seen[j] {
					continue
				}
				seen[j] = true
				edges[i] = append(edges[i], j)
			}
		}
	}

	const (
		white = iota
		grey
		black
	)
	color := make([]int, len(regs))
	var stack []int
	var visit func(i int) error
	visit = func(i int) error {
		switch color[i] {
		case black:
			return nil
		case grey:
			// stack holds the current DFS path; the cycle is its tail from i.
			var path []string
			for k := len(stack) - 1; k >= 0; k-- {
				if stack[k] == i {
					for _, j := range stack[k:] {
						path = append(path, regs[j].Plugin.Name())
					}
					break
				}
			}
			return &CycleError{Path: append(path, regs[i].Plugin.Name())}
		}
		color[i] = grey
		stack = append(stack, i)
		for _, j := range edges[i] {
			if err := visit(j); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = black
		g.order = append(g.order, regs[i])
		return nil
	}
	for i := range regs {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	return g, nil
}

// Order returns the plugins providers-first. The slice is a copy.
func (g *Graph) Order() []Registered {
	return append([]Registered(nil), g.order...)
}

// Providers returns the plugins providing c, in registration order. The
// slice is a copy.
func (g *Graph) Providers(c Capability) []Registered {
	return append([]Registered(nil), g.providers[c]...)
}

// Lookup finds a plugin by name.
func (g *Graph) Lookup(name string) (Registered, bool) {
	for _, r := range g.order {
		if r.Plugin.Name() == name {
			return r, true
		}
	}
	return Registered{}, false
}
