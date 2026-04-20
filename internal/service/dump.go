package service

import (
	"encoding/csv"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// DumpManager receives data at key pipeline stages for debug analysis.
type DumpManager interface {
	OnRawCalls(calls []model.RawCall)
	OnResolved(relations []model.ResolvedRelation, hints []model.UnresolvedHint)
	OnAllRelations(heritage, overrides, implements []model.ResolvedRelation)
	OnAnnotations(nodes []model.Node, edges []model.Edge)
	OnRoutes(nodes []model.Node)
}

// NopDumpManager does nothing (debug=false).
type NopDumpManager struct{}

func (NopDumpManager) OnRawCalls([]model.RawCall)                                 {}
func (NopDumpManager) OnResolved([]model.ResolvedRelation, []model.UnresolvedHint) {}
func (NopDumpManager) OnAllRelations(_, _, _ []model.ResolvedRelation)             {}
func (NopDumpManager) OnAnnotations([]model.Node, []model.Edge)                   {}
func (NopDumpManager) OnRoutes([]model.Node)                                      {}

// FileDumpManager writes CSV files to .fcg/debug/.
type FileDumpManager struct {
	debugDir string
}

func NewFileDumpManager(projectPath string) *FileDumpManager {
	dir := filepath.Join(projectPath, ".fcg", "debug")
	os.RemoveAll(dir)
	os.MkdirAll(dir, model.DirectoryPermission)
	return &FileDumpManager{debugDir: dir}
}

func (d *FileDumpManager) createCSV(name string, header []string) (*csv.Writer, *os.File) {
	f, err := os.Create(filepath.Join(d.debugDir, name))
	if err != nil {
		return nil, nil
	}
	w := csv.NewWriter(f)
	w.Write(header)
	return w, f
}

var rawcallsHeader = []string{"file_path", "line", "called_name", "receiver_expr", "caller_name", "arg_count"}
var callsHeader = []string{"resolved_by", "confidence", "candidates", "source_id", "target_id", "file_path", "line", "arg_count", "called_name", "receiver_expr"}
var hintsHeader = []string{"file_path", "line", "called_name", "receiver_expr", "hint_type", "candidate_count"}

func (d *FileDumpManager) OnRawCalls(calls []model.RawCall) {
	writers := map[string]*csv.Writer{}
	files := map[string]*os.File{}

	for _, c := range calls {
		group := "with_receiver"
		if c.ReceiverExpr == "" {
			group = "no_receiver"
		}
		if _, ok := writers[group]; !ok {
			w, f := d.createCSV("rawcalls_"+group+".csv", rawcallsHeader)
			if w == nil {
				continue
			}
			writers[group] = w
			files[group] = f
		}
		writers[group].Write([]string{
			c.FilePath, strconv.Itoa(c.Line), c.CalledName,
			c.ReceiverExpr, c.CallerName, strconv.Itoa(c.ArgCount),
		})
	}
	for g, w := range writers {
		w.Flush()
		files[g].Close()
	}
	log.Printf("[debug] dumped %d rawcalls to .fcg/debug/ (%d groups)", len(calls), len(writers))
}

func (d *FileDumpManager) OnResolved(relations []model.ResolvedRelation, hints []model.UnresolvedHint) {
	writers := map[string]*csv.Writer{}
	files := map[string]*os.File{}

	for _, r := range relations {
		group := r.ResolvedBy
		if _, ok := writers[group]; !ok {
			w, f := d.createCSV("calls_"+group+".csv", callsHeader)
			if w == nil {
				continue
			}
			writers[group] = w
			files[group] = f
		}
		m := r.Metadata
		if m == nil {
			m = map[string]string{}
		}
		writers[group].Write([]string{
			r.ResolvedBy,
			strconv.FormatFloat(r.Confidence, 'f', 3, 64),
			strconv.Itoa(r.Candidates),
			r.SourceID, r.TargetID,
			m["file_path"], strconv.Itoa(r.Line),
			m["arg_count"], m["called_name"], m["receiver_expr"],
		})
	}
	for g, w := range writers {
		w.Flush()
		files[g].Close()
	}

	// hints
	if w, f := d.createCSV("hints.csv", hintsHeader); w != nil {
		for _, h := range hints {
			w.Write([]string{
				h.FilePath, strconv.Itoa(h.Line), h.Method,
				h.ReceiverExpr, h.HintType, strconv.Itoa(h.CandidateCount),
			})
		}
		w.Flush()
		f.Close()
	}

	log.Printf("[debug] dumped %d calls (%d groups), %d hints to .fcg/debug/", len(relations), len(writers), len(hints))
}

var relationHeader = []string{"source_id", "target_id", "kind", "confidence", "resolved_by", "candidates"}

func (d *FileDumpManager) OnAllRelations(heritage, overrides, implements []model.ResolvedRelation) {
	dump := func(name string, relations []model.ResolvedRelation) {
		if len(relations) == 0 {
			return
		}
		w, f := d.createCSV(name, relationHeader)
		if w == nil {
			return
		}
		for _, r := range relations {
			w.Write([]string{
				r.SourceID, r.TargetID, string(r.Kind),
				strconv.FormatFloat(r.Confidence, 'f', 3, 64),
				r.ResolvedBy, strconv.Itoa(r.Candidates),
			})
		}
		w.Flush()
		f.Close()
	}
	dump("heritage.csv", heritage)
	dump("overrides.csv", overrides)
	dump("implements.csv", implements)
	log.Printf("[debug] dumped %d heritage, %d overrides, %d implements to .fcg/debug/", len(heritage), len(overrides), len(implements))
}

func (d *FileDumpManager) OnAnnotations(nodes []model.Node, edges []model.Edge) {
	if len(nodes) == 0 {
		return
	}
	header := []string{"id", "name", "params", "category", "framework", "file_path", "line", "source_id"}
	writer, file := d.createCSV("annotations.csv", header)
	if writer == nil {
		return
	}
	// Build edge lookup: annotation_id -> source_id
	sourceMap := make(map[string]string, len(edges))
	for _, edge := range edges {
		sourceMap[edge.TargetID] = edge.SourceID
	}
	for _, node := range nodes {
		props := node.Properties
		writer.Write([]string{
			node.ID,
			propStr(props, "name"),
			propStr(props, "params"),
			propStr(props, "category"),
			propStr(props, "framework"),
			propStr(props, "file_path"),
			propStr(props, "line"),
			sourceMap[node.ID],
		})
	}
	writer.Flush()
	file.Close()
	log.Printf("[debug] dumped %d annotations to .fcg/debug/annotations.csv", len(nodes))
}

func (d *FileDumpManager) OnRoutes(nodes []model.Node) {
	if len(nodes) == 0 {
		return
	}
	header := []string{"id", "method", "path", "handler", "file_path", "line"}
	writer, file := d.createCSV("routes.csv", header)
	if writer == nil {
		return
	}
	for _, node := range nodes {
		props := node.Properties
		writer.Write([]string{
			node.ID,
			propStr(props, "method"),
			propStr(props, "path"),
			propStr(props, "handler"),
			propStr(props, "file_path"),
			propStr(props, "line"),
		})
	}
	writer.Flush()
	file.Close()
	log.Printf("[debug] dumped %d routes to .fcg/debug/routes.csv", len(nodes))
}

func propStr(properties map[string]any, key string) string {
	if properties == nil {
		return ""
	}
	value, ok := properties[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}
