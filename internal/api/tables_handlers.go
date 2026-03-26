package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/garett/aiprod/internal/tables"
	"github.com/go-chi/chi/v5"
)

func (s *Server) RegisterTablesRoutes(r chi.Router, store *tables.Store) {
	r.Route("/tables", func(r chi.Router) {
		r.Post("/", s.handleTableCreate(store))
		r.Get("/", s.handleTableList(store))
		r.Get("/{name}", s.handleTableGet(store))
		r.Delete("/{name}", s.handleTableDelete(store))
		r.Post("/{name}/rows", s.handleTableInsert(store))
		r.Get("/{name}/rows", s.handleTableQuery(store))
		r.Patch("/{name}/rows/{rowid}", s.handleTableUpdateRow(store))
		r.Delete("/{name}/rows/{rowid}", s.handleTableDeleteRow(store))
		r.Post("/{name}/query", s.handleTableSQL(store))
		r.Post("/{name}/import", s.handleTableImport(store))
		r.Get("/{name}/export", s.handleTableExport(store))
	})
}

func (s *Server) handleTableCreate(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name        string           `json:"name"`
			Description string           `json:"description"`
			Columns     []tables.Column  `json:"columns"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.Name == "" || len(req.Columns) == 0 {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "name and columns are required")
			return
		}
		t, err := store.Create(req.Name, req.Description, req.Columns)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, t)
	}
}

func (s *Server) handleTableList(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		list, err := store.List()
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, list)
	}
}

func (s *Server) handleTableGet(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		t, err := store.Get(name)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		if t == nil {
			WriteError(w, http.StatusNotFound, "NOT_FOUND", "Table not found")
			return
		}
		WriteJSON(w, http.StatusOK, t)
	}
}

func (s *Server) handleTableDelete(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if err := store.Delete(name); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (s *Server) handleTableInsert(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		rowID, err := store.InsertRow(name, data)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]interface{}{"_rowid": rowID})
	}
}

func (s *Server) handleTableQuery(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		q := r.URL.Query()
		limit, _ := strconv.Atoi(q.Get("limit"))
		offset, _ := strconv.Atoi(q.Get("offset"))
		rows, err := store.QueryRows(name, q.Get("where"), q.Get("sort"), limit, offset)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, rows)
	}
}

func (s *Server) handleTableUpdateRow(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		rowID, err := strconv.ParseInt(chi.URLParam(r, "rowid"), 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid rowid")
			return
		}
		var data map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if err := store.UpdateRow(name, rowID, data); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "updated"})
	}
}

func (s *Server) handleTableDeleteRow(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		rowID, err := strconv.ParseInt(chi.URLParam(r, "rowid"), 10, 64)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid rowid")
			return
		}
		if err := store.DeleteRow(name, rowID); err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (s *Server) handleTableSQL(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		var req struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid JSON")
			return
		}
		_ = name // Table name available for context, but query runs directly
		rows, err := store.ExecSQL(name, req.Query)
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, rows)
	}
}

func (s *Server) handleTableImport(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")

		reader := csv.NewReader(r.Body)
		records, err := reader.ReadAll()
		if err != nil {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "Invalid CSV: "+err.Error())
			return
		}
		if len(records) < 2 {
			WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "CSV must have header row and at least one data row")
			return
		}

		headers := records[0]
		dataRows := records[1:]
		count, err := store.ImportCSV(name, headers, dataRows)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"imported": count})
	}
}

func (s *Server) handleTableExport(store *tables.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "json"
		}

		rows, err := store.ExportJSON(name)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "INTERNAL", err.Error())
			return
		}

		if format == "csv" {
			w.Header().Set("Content-Type", "text/csv")
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.csv\"", name))
			writer := csv.NewWriter(w)
			if len(rows) > 0 {
				// Write headers
				headers := []string{}
				for k := range rows[0] {
					headers = append(headers, k)
				}
				writer.Write(headers)
				for _, row := range rows {
					record := make([]string, len(headers))
					for i, h := range headers {
						record[i] = fmt.Sprintf("%v", row[h])
					}
					writer.Write(record)
				}
			}
			writer.Flush()
			return
		}

		WriteJSON(w, http.StatusOK, rows)
	}
}
