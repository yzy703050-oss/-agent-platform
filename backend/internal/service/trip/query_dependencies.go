package trip

import "agent-platform/backend/internal/model"

func ValidateQueryDependencies(queries []model.TravelQuery) error {
	graph := map[string][]string{}
	for _, q := range queries {
		key := q.PlanID + "/" + q.TaskID
		for _, dep := range q.DependsOn {
			graph[key] = append(graph[key], q.PlanID+"/"+dep)
		}
	}
	visited, active := map[string]bool{}, map[string]bool{}
	var visit func(string) bool
	visit = func(key string) bool {
		if active[key] {
			return false
		}
		if visited[key] {
			return true
		}
		active[key] = true
		for _, dep := range graph[key] {
			if !visit(dep) {
				return false
			}
		}
		active[key] = false
		visited[key] = true
		return true
	}
	for key := range graph {
		if !visit(key) {
			return invalid("query dependencies contain a cycle")
		}
	}
	return nil
}
