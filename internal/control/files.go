package control

import (
	"net/http"
	"strconv"

	"undertow/internal/pivot"
)

// filesHandler returns typed metadata produced by the target agent. It does
// not infer entries from the text console's ls output.
func (m *Manager) filesHandler(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if key != "path" && key != "offset" || len(values) != 1 {
			http.Error(w, "invalid file listing query", http.StatusBadRequest)
			return
		}
	}
	path := query.Get("path")
	if len(path) > 4096 {
		http.Error(w, "file path too long", http.StatusBadRequest)
		return
	}
	args := []string{path}
	if offset := query.Get("offset"); offset != "" {
		parsed, err := strconv.Atoi(offset)
		if err != nil || parsed < 0 || parsed > 100000 {
			http.Error(w, "invalid file listing offset", http.StatusBadRequest)
			return
		}
		args = append(args, offset)
	}
	agent := m.Get(r.PathValue("id"))
	if agent == nil {
		http.Error(w, "agent is not connected", http.StatusNotFound)
		return
	}
	result, err := pivot.ExecuteRequest(r.Context(), agent, pivot.ExecRequest{Builtin: "file-list", Args: args})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if result.Error != "" {
		http.Error(w, result.Error, http.StatusUnprocessableEntity)
		return
	}
	if result.Files == nil {
		http.Error(w, "agent does not provide structured file listing", http.StatusBadGateway)
		return
	}
	jsonReply(w, http.StatusOK, result.Files)
}
