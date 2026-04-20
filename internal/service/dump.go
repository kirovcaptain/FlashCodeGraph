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
}

// NopDumpManager does nothing (debug=false).
type NopDumpManager struct{}

func (NopDumpManager) OnRawCalls([]model.RawCall)                                 {}
func (NopDumpManager) OnResolved([]model.ResolvedRelation, []model.UnresolvedHint) {}
func (NopDumpManager) OnAllRelations(_, _, _ []model.ResolvedRelation)             {}

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
