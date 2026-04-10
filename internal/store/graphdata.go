package store

import "fmt"

// GraphData is the JSON structure consumed by the D3.js dashboard.
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Links []GraphLink `json:"links"`
	Stats GraphStats  `json:"stats"`
}

// GraphNode represents a memory or entity in the force graph.
type GraphNode struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Group       string  `json:"group"`
	Type        string  `json:"type"` // "memory" or "entity"
	Kind        string  `json:"kind,omitempty"`
	Importance  float64 `json:"importance,omitempty"`
	Val         int     `json:"val"` // node radius
	Color       string  `json:"color"`
	Connections int     `json:"connections"`
}

// GraphLink represents an edge.
type GraphLink struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
}

// GraphStats summarizes the graph.
type GraphStats struct {
	Entities   int `json:"entities"`
	Relations  int `json:"relations"`
	Memories   int `json:"memories"`
	TotalNodes int `json:"total_nodes"`
	TotalLinks int `json:"total_links"`
}

var kindColors = map[string]string{
	"core": "#fbbf24", "semantic": "#818cf8",
	"episodic": "#6b7280", "procedural": "#34d399",
}
var entityColors = map[string]string{
	"person": "#4a9eff", "location": "#4ecdc4",
	"organization": "#ff6b6b", "concept": "#a855f7",
}

// BuildGraphData constructs the force-directed graph JSON.
func (s *Store) BuildGraphData(project string) (*GraphData, error) {
	if project == "" {
		project = DefaultProject
	}
	data := &GraphData{}

	// Memory nodes.
	memRows, err := s.db.Query(`
		SELECT id, content, kind, importance FROM memories
		WHERE project = ? AND invalid_at IS NULL
		ORDER BY created_at DESC LIMIT 200`, project)
	if err != nil {
		return nil, err
	}
	defer memRows.Close()

	memIDs := map[int64]bool{}
	for memRows.Next() {
		var id int64
		var content, kind string
		var imp float64
		memRows.Scan(&id, &content, &kind, &imp)
		memIDs[id] = true
		snippet := content
		if len(snippet) > 80 {
			snippet = snippet[:80] + "..."
		}
		color := kindColors[kind]
		if color == "" {
			color = "#6b7280"
		}
		data.Nodes = append(data.Nodes, GraphNode{
			ID: fmt.Sprintf("m_%d", id), Name: snippet, Group: "memory_" + kind,
			Type: "memory", Kind: kind, Importance: imp,
			Val: 4 + int(imp*4), Color: color,
		})
		data.Stats.Memories++
	}

	// Entity nodes (only significant ones — connected to >=1 memory).
	entRows, err := s.db.Query(`
		SELECT e.id, e.name, e.entity_type, COUNT(me.memory_id) as cnt
		FROM entities e
		JOIN memory_entities me ON e.id = me.entity_id
		WHERE e.project = ?
		GROUP BY e.id
		HAVING cnt > 0
		ORDER BY cnt DESC LIMIT 100`, project)
	if err != nil {
		return nil, err
	}
	defer entRows.Close()

	entNames := map[int64]string{}
	for entRows.Next() {
		var id int64
		var name, etype string
		var cnt int
		entRows.Scan(&id, &name, &etype, &cnt)
		entNames[id] = name
		color := entityColors[etype]
		if color == "" {
			color = "#a855f7"
		}
		data.Nodes = append(data.Nodes, GraphNode{
			ID: fmt.Sprintf("e_%d", id), Name: name, Group: etype,
			Type: "entity", Val: 3 + cnt, Color: color, Connections: cnt,
		})
		data.Stats.Entities++
	}

	// Links: memory ↔ entity.
	linkRows, err := s.db.Query(`
		SELECT me.memory_id, me.entity_id FROM memory_entities me
		JOIN entities e ON me.entity_id = e.id
		WHERE e.project = ?`, project)
	if err != nil {
		return nil, err
	}
	defer linkRows.Close()

	for linkRows.Next() {
		var memID, entID int64
		linkRows.Scan(&memID, &entID)
		if !memIDs[memID] {
			continue
		}
		if _, ok := entNames[entID]; !ok {
			continue
		}
		data.Links = append(data.Links, GraphLink{
			Source: fmt.Sprintf("m_%d", memID),
			Target: fmt.Sprintf("e_%d", entID),
			Label:  "menciona", Value: 0.3,
		})
	}

	// Links: entity → entity (relations).
	relRows, err := s.db.Query(`
		SELECT se.id, r.predicate, oe.id, r.confidence
		FROM relations r
		JOIN entities se ON r.subject_entity_id = se.id
		JOIN entities oe ON r.object_entity_id = oe.id
		WHERE r.project = ? AND r.invalid_at IS NULL`, project)
	if err != nil {
		return nil, err
	}
	defer relRows.Close()

	for relRows.Next() {
		var subID, objID int64
		var pred string
		var conf float64
		relRows.Scan(&subID, &pred, &objID, &conf)
		// Only include links where both entities are in the node set.
		if _, ok := entNames[subID]; !ok {
			continue
		}
		if _, ok := entNames[objID]; !ok {
			continue
		}
		data.Links = append(data.Links, GraphLink{
			Source: fmt.Sprintf("e_%d", subID),
			Target: fmt.Sprintf("e_%d", objID),
			Label:  pred, Value: conf,
		})
		data.Stats.Relations++
	}

	data.Stats.TotalNodes = len(data.Nodes)
	data.Stats.TotalLinks = len(data.Links)

	if data.Nodes == nil {
		data.Nodes = []GraphNode{}
	}
	if data.Links == nil {
		data.Links = []GraphLink{}
	}
	return data, nil
}
