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
	shards          [ShardCount]shard
	methodsByClass  map[string][]model.Symbol    // classQN → methods
	fieldsByOwner   map[string][]model.FieldInfo // classQN → fields
	methodsMutex    sync.RWMutex
	fieldsMutex     sync.RWMutex
}

type shard struct {
	mutex           sync.RWMutex
	byName          map[string][]model.Symbol
	byQualifiedName map[string][]model.Symbol
	byFile          map[string][]model.Symbol
	byID            map[string]*model.Symbol
}

// NewSymbolTable creates an empty SymbolTable.
func NewSymbolTable() *SymbolTable {
	table := &SymbolTable{
		methodsByClass: make(map[string][]model.Symbol),
		fieldsByOwner:  make(map[string][]model.FieldInfo),
	}
	for i := range table.shards {
		table.shards[i] = shard{
			byName:          make(map[string][]model.Symbol),
			byQualifiedName: make(map[string][]model.Symbol),
			byFile:          make(map[string][]model.Symbol),
			byID:            make(map[string]*model.Symbol),
		}
	}
	return table
}

// Add inserts a symbol into the table (concurrent-safe).
func (table *SymbolTable) Add(symbol model.Symbol) {
	shardIndex := shardFor(symbol.Name)
	shard := &table.shards[shardIndex]
	shard.mutex.Lock()
	defer shard.mutex.Unlock()

	shard.byName[symbol.Name] = append(shard.byName[symbol.Name], symbol)
	if symbol.QualifiedName != "" {
		shard.byQualifiedName[symbol.QualifiedName] = append(shard.byQualifiedName[symbol.QualifiedName], symbol)
	}
	shard.byFile[symbol.FilePath] = append(shard.byFile[symbol.FilePath], symbol)
	symbolCopy := symbol
	shard.byID[symbol.ID] = &symbolCopy

	// Index methods by owner class for FindMethodsByQualifiedName
	if symbol.Kind == constants.KindFunction && strings.Contains(symbol.QualifiedName, ".") {
		ownerClassQN := symbol.QualifiedName[:strings.LastIndex(symbol.QualifiedName, ".")]
		table.methodsMutex.Lock()
		table.methodsByClass[ownerClassQN] = append(table.methodsByClass[ownerClassQN], symbol)
		table.methodsMutex.Unlock()
	}
}

// AddBatch inserts multiple symbols (concurrent-safe).
func (table *SymbolTable) AddBatch(symbols []model.Symbol) {
	for _, symbol := range symbols {
		table.Add(symbol)
	}
}

// FindByName returns all symbols with the given name.
func (table *SymbolTable) FindByName(name string) []model.Symbol {
	shardIndex := shardFor(name)
	shard := &table.shards[shardIndex]
	shard.mutex.RLock()
	defer shard.mutex.RUnlock()
	return shard.byName[name]
}

// FindByQualifiedName returns symbols matching the qualified name.
// The qualified name is stored in the same shard as symbol.Name (which is the
// shard key used by Add), so we use lastSegment to derive the same shard index.
// This works because lastSegment("com.example.UserService") = "UserService" = symbol.Name.
// For correctness, we also fall back to scanning all shards if the primary lookup misses.
func (table *SymbolTable) FindByQualifiedName(qualifiedName string) []model.Symbol {
	// Primary lookup: use lastSegment as shard key (matches symbol.Name in most cases)
	name := lastSegment(qualifiedName)
	shardIndex := shardFor(name)
	shard := &table.shards[shardIndex]
	shard.mutex.RLock()
	result := shard.byQualifiedName[qualifiedName]
	shard.mutex.RUnlock()
	if len(result) > 0 {
		return result
	}

	// Fallback: scan all shards in case shard key doesn't match symbol.Name
	for i := range table.shards {
		s := &table.shards[i]
		s.mutex.RLock()
		if found := s.byQualifiedName[qualifiedName]; len(found) > 0 {
			s.mutex.RUnlock()
			return found
		}
		s.mutex.RUnlock()
	}
	return nil
}

// FindByFile returns all symbols in a file.
func (table *SymbolTable) FindByFile(filePath string) []model.Symbol {
	var result []model.Symbol
	for i := range table.shards {
		shard := &table.shards[i]
		shard.mutex.RLock()
		result = append(result, shard.byFile[filePath]...)
		shard.mutex.RUnlock()
	}
	return result
}

// FindByID returns a symbol by ID.
// All returns all symbols in the table.
func (table *SymbolTable) All() []model.Symbol {
	var all []model.Symbol
	for i := range table.shards {
		table.shards[i].mutex.RLock()
		for _, syms := range table.shards[i].byName {
			all = append(all, syms...)
		}
		table.shards[i].mutex.RUnlock()
	}
	return all
}

func (table *SymbolTable) FindByID(id string) *model.Symbol {
	for i := range table.shards {
		shard := &table.shards[i]
		shard.mutex.RLock()
		symbol := shard.byID[id]
		shard.mutex.RUnlock()
		if symbol != nil {
			return symbol
		}
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

// FindMethodsByQualifiedName returns all functions whose QualifiedName starts with classQN + ".".
func (table *SymbolTable) FindMethodsByQualifiedName(classQN string) []model.Symbol {
	table.methodsMutex.RLock()
	defer table.methodsMutex.RUnlock()
	return table.methodsByClass[classQN]
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
	for _, field := range table.fieldsByOwner[ownerQualifiedName] {
		if field.Name == fieldName {
			return &field
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
