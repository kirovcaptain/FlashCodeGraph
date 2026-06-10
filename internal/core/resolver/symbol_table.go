// Package resolver resolves raw calls/heritage into typed relationships with confidence.
package resolver

import (
	"strings"
	"sync"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// ShardCount is the number of shards for the SymbolTable.
const ShardCount = 64

// SymbolTable provides concurrent-safe symbol lookup by name, qualified name, file, and ID.
type SymbolTable struct {
	shards         [ShardCount]shard
	methodsByClass map[string][]int // classQualifiedName → encoded global indices (shardIndex<<24 | localIndex)
	fieldsByOwner  map[string][]model.FieldInfo
	methodsMutex   sync.RWMutex
	fieldsMutex    sync.RWMutex
	// reExportIndex maps a re-export qualified name to the target symbol ID.
	// Written during propagateExports (serial), read-only during ResolveCalls.
	reExportIndex map[string]string
}

type shard struct {
	mutex           sync.RWMutex
	symbols         []model.Symbol
	byName          map[string][]int32
	byQualifiedName map[string][]int32
	byFile          map[string][]int32
	byID            map[string]int32
}

// NewSymbolTable creates an empty SymbolTable.
func NewSymbolTable() *SymbolTable {
	table := &SymbolTable{
		methodsByClass: make(map[string][]int),
		fieldsByOwner:  make(map[string][]model.FieldInfo),
		reExportIndex:  make(map[string]string),
	}
	for i := range table.shards {
		table.shards[i] = shard{
			symbols:         make([]model.Symbol, 0, 256),
			byName:          make(map[string][]int32),
			byQualifiedName: make(map[string][]int32),
			byFile:          make(map[string][]int32),
			byID:            make(map[string]int32),
		}
	}
	return table
}

// Add inserts a symbol into the table (concurrent-safe).
func (table *SymbolTable) Add(symbol model.Symbol) {
	shardIndex := shardFor(symbol.Name)
	targetShard := &table.shards[shardIndex]
	targetShard.mutex.Lock()

	localIndex := int32(len(targetShard.symbols))
	targetShard.symbols = append(targetShard.symbols, symbol)

	targetShard.byName[symbol.Name] = append(targetShard.byName[symbol.Name], localIndex)
	if symbol.QualifiedName != "" {
		targetShard.byQualifiedName[symbol.QualifiedName] = append(targetShard.byQualifiedName[symbol.QualifiedName], localIndex)
	}
	targetShard.byFile[symbol.FilePath] = append(targetShard.byFile[symbol.FilePath], localIndex)
	targetShard.byID[symbol.ID] = localIndex

	targetShard.mutex.Unlock()

	// Index methods by owner class for FindMethodsByQualifiedName
	if symbol.Kind == constants.KindFunction && strings.Contains(symbol.QualifiedName, ".") {
		ownerClassQualifiedName := symbol.QualifiedName[:strings.LastIndex(symbol.QualifiedName, ".")]
		globalIndex := int(shardIndex)<<24 | int(localIndex)
		table.methodsMutex.Lock()
		table.methodsByClass[ownerClassQualifiedName] = append(table.methodsByClass[ownerClassQualifiedName], globalIndex)
		table.methodsMutex.Unlock()
	}
}

// AddBatch inserts multiple symbols (concurrent-safe).
func (table *SymbolTable) AddBatch(symbols []model.Symbol) {
	for i := range symbols {
		table.Add(symbols[i])
	}
}

// FindByName returns all symbols with the given name.
func (table *SymbolTable) FindByName(name string) []model.Symbol {
	shardIndex := shardFor(name)
	targetShard := &table.shards[shardIndex]
	targetShard.mutex.RLock()
	indices := targetShard.byName[name]
	if len(indices) == 0 {
		targetShard.mutex.RUnlock()
		return nil
	}
	result := make([]model.Symbol, len(indices))
	for i, localIndex := range indices {
		result[i] = targetShard.symbols[localIndex]
	}
	targetShard.mutex.RUnlock()
	return result
}

// FindByQualifiedName returns symbols matching the qualified name.
// The qualified name is stored in the same shard as symbol.Name (which is the
// shard key used by Add), so we use lastSegment to derive the same shard index.
// For correctness, we also fall back to scanning all shards if the primary lookup misses.
func (table *SymbolTable) FindByQualifiedName(qualifiedName string) []model.Symbol {
	// Primary lookup: use lastSegment as shard key (matches symbol.Name in most cases)
	name := lastSegment(qualifiedName)
	shardIndex := shardFor(name)
	targetShard := &table.shards[shardIndex]
	targetShard.mutex.RLock()
	indices := targetShard.byQualifiedName[qualifiedName]
	if len(indices) > 0 {
		result := make([]model.Symbol, len(indices))
		for i, localIndex := range indices {
			result[i] = targetShard.symbols[localIndex]
		}
		targetShard.mutex.RUnlock()
		return result
	}
	targetShard.mutex.RUnlock()

	// Fallback: scan all shards in case shard key doesn't match symbol.Name
	for i := range table.shards {
		currentShard := &table.shards[i]
		currentShard.mutex.RLock()
		if foundIndices := currentShard.byQualifiedName[qualifiedName]; len(foundIndices) > 0 {
			result := make([]model.Symbol, len(foundIndices))
			for j, localIndex := range foundIndices {
				result[j] = currentShard.symbols[localIndex]
			}
			currentShard.mutex.RUnlock()
			return result
		}
		currentShard.mutex.RUnlock()
	}
	return nil
}

// FindByFile returns all symbols in a file.
func (table *SymbolTable) FindByFile(filePath string) []model.Symbol {
	var result []model.Symbol
	for i := range table.shards {
		currentShard := &table.shards[i]
		currentShard.mutex.RLock()
		indices := currentShard.byFile[filePath]
		for _, localIndex := range indices {
			result = append(result, currentShard.symbols[localIndex])
		}
		currentShard.mutex.RUnlock()
	}
	return result
}

// All returns all symbols in the table.
func (table *SymbolTable) All() []model.Symbol {
	var all []model.Symbol
	for i := range table.shards {
		table.shards[i].mutex.RLock()
		all = append(all, table.shards[i].symbols...)
		table.shards[i].mutex.RUnlock()
	}
	return all
}

// FindByID returns a symbol by ID.
func (table *SymbolTable) FindByID(id string) *model.Symbol {
	for i := range table.shards {
		currentShard := &table.shards[i]
		currentShard.mutex.RLock()
		localIndex, exists := currentShard.byID[id]
		if exists {
			symbol := &currentShard.symbols[localIndex]
			currentShard.mutex.RUnlock()
			return symbol
		}
		currentShard.mutex.RUnlock()
	}
	return nil
}

func shardFor(name string) int {
	if len(name) == 0 {
		return 0
	}
	// FNV-1a hash for better distribution
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619
	}
	return int(h) % ShardCount
}

func lastSegment(qualifiedName string) string {
	for i := len(qualifiedName) - 1; i >= 0; i-- {
		if qualifiedName[i] == '.' {
			return qualifiedName[i+1:]
		}
	}
	return qualifiedName
}

// FindMethodsByQualifiedName returns all functions whose owner class matches classQualifiedName.
func (table *SymbolTable) FindMethodsByQualifiedName(classQualifiedName string) []model.Symbol {
	table.methodsMutex.RLock()
	globalIndices := table.methodsByClass[classQualifiedName]
	if len(globalIndices) == 0 {
		table.methodsMutex.RUnlock()
		return nil
	}
	// Copy indices under lock to avoid holding methodsMutex while acquiring shard locks
	indicesCopy := make([]int, len(globalIndices))
	copy(indicesCopy, globalIndices)
	table.methodsMutex.RUnlock()

	result := make([]model.Symbol, 0, len(indicesCopy))
	for _, globalIndex := range indicesCopy {
		shardIndex := globalIndex >> 24
		localIndex := int32(globalIndex & 0xFFFFFF)
		currentShard := &table.shards[shardIndex]
		currentShard.mutex.RLock()
		if int(localIndex) < len(currentShard.symbols) {
			result = append(result, currentShard.symbols[localIndex])
		}
		currentShard.mutex.RUnlock()
	}
	return result
}

// AddField registers a field for a class/struct.
func (table *SymbolTable) AddField(ownerQualifiedName string, field model.FieldInfo) {
	table.fieldsMutex.Lock()
	defer table.fieldsMutex.Unlock()
	table.fieldsByOwner[ownerQualifiedName] = append(table.fieldsByOwner[ownerQualifiedName], field)
}

// FindFieldByOwner returns a specific field of a class by name.
func (table *SymbolTable) FindFieldByOwner(ownerQualifiedName, fieldName string) *model.FieldInfo {
	table.fieldsMutex.RLock()
	defer table.fieldsMutex.RUnlock()
	for i := range table.fieldsByOwner[ownerQualifiedName] {
		if table.fieldsByOwner[ownerQualifiedName][i].Name == fieldName {
			return &table.fieldsByOwner[ownerQualifiedName][i]
		}
	}
	return nil
}

// FindFieldsByOwner returns all fields of a class/struct.
func (table *SymbolTable) FindFieldsByOwner(ownerQualifiedName string) []model.FieldInfo {
	table.fieldsMutex.RLock()
	defer table.fieldsMutex.RUnlock()
	return table.fieldsByOwner[ownerQualifiedName]
}

// AddReExport registers a re-export qualified name pointing to a target symbol ID.
// Called during propagateExports (serial phase); no lock needed.
func (table *SymbolTable) AddReExport(reExportQualifiedName, targetSymbolID string) {
	table.reExportIndex[reExportQualifiedName] = targetSymbolID
}

// HasReExport checks if a re-export qualified name is already registered.
func (table *SymbolTable) HasReExport(reExportQualifiedName string) bool {
	_, exists := table.reExportIndex[reExportQualifiedName]
	return exists
}

// GetReExport returns the target symbol ID for a re-export qualified name.
func (table *SymbolTable) GetReExport(reExportQualifiedName string) (string, bool) {
	targetID, exists := table.reExportIndex[reExportQualifiedName]
	return targetID, exists
}

// FindByQualifiedNameWithReExport extends FindByQualifiedName to also check reExportIndex.
// If direct lookup fails, checks if the qualified name is a re-export alias and returns the target symbol.
func (table *SymbolTable) FindByQualifiedNameWithReExport(qualifiedName string) []model.Symbol {
	results := table.FindByQualifiedName(qualifiedName)
	if len(results) > 0 {
		return results
	}
	targetID, exists := table.reExportIndex[qualifiedName]
	if !exists {
		return nil
	}
	symbol := table.FindByID(targetID)
	if symbol == nil {
		return nil
	}
	return []model.Symbol{*symbol}
}

// FindExportedByFile returns all exported symbols defined in the given file.
func (table *SymbolTable) FindExportedByFile(filePath string) []model.Symbol {
	allInFile := table.FindByFile(filePath)
	var exported []model.Symbol
	for _, symbol := range allInFile {
		if symbol.IsExported {
			exported = append(exported, symbol)
		}
	}
	return exported
}
