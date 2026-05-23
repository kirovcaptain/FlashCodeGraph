package resolver

import "github.com/kirovcaptain/FlashCodeGraph/internal/model"

// LanguageHelper provides language-specific call resolution strategies.
// The resolver delegates to the appropriate helper based on call.Language.
type LanguageHelper interface {
	// ResolveSuperCall handles parent class method calls via super/base keyword.
	// Called when ReceiverExpr is "super" (or language equivalent).
	ResolveSuperCall(call model.RawCall, funcCandidates []model.Symbol, heritage []model.RawHeritage, envs map[string]*model.TypeEnv, callerID string) ([]model.ResolvedRelation, bool)

	// NarrowByScope narrows multiple candidates to those visible from the call site,
	// using language-specific import/package/module rules.
	NarrowByScope(matched []model.Symbol, call model.RawCall, env *model.TypeEnv, symbolTable *SymbolTable) []model.Symbol

	// ResolveReceiverFallback is the last-resort resolution when ReceiverExpr is present
	// but all standard strategies have failed. Language-specific rules may still resolve the call.
	ResolveReceiverFallback(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *SymbolTable) ([]model.ResolvedRelation, bool)

	// ResolveImplicitSelfCall handles method calls where the receiver (this/self)
	// is omitted in source code.
	ResolveImplicitSelfCall(call model.RawCall, funcCandidates []model.Symbol, envs map[string]*model.TypeEnv, callerID string, symbolTable *SymbolTable) ([]model.ResolvedRelation, bool)

	// ShouldFallthrough controls whether the resolver falls through from
	// receiver-based matching to no-receiver matching when all receiver strategies fail.
	ShouldFallthrough() bool

	// FilterGenerated removes auto-generated symbols that should not participate
	// in fallback matching (to avoid false positives from generated code).
	FilterGenerated(candidates []model.Symbol) []model.Symbol

	// IsTypeAssignable checks if a value of argType can be passed to a parameter
	// of paramType, using language-specific type assignment rules (boxing, hierarchy, etc).
	IsTypeAssignable(argType, paramType string) bool

	// ResolveOverload selects the best candidate when multiple overloads match
	// the same argument count and types. Uses language-specific type hierarchy
	// to pick the most specific overload.
	ResolveOverload(candidates []model.Symbol, argTypes []string) *model.Symbol

	// InferStringConcat returns true if the expression is definitely a string
	// concatenation (e.g., contains + and a string literal).
	InferStringConcat(expr string) bool

	// LookupMethodReturn returns the return type of a method on a given type.
	// Used by inferExprType for chain resolution (e.g., Exception.getMessage → String).
	LookupMethodReturn(typeName, methodName string) (string, bool)

	// BuildExternalQualifiedName constructs a fully qualified name for an external method.
	// e.g. ("Stream", "map") → "java.util.stream.Stream.map"
	BuildExternalQualifiedName(typeName, methodName string) string

	// IsConstructor returns true if the method is a constructor for the given class.
	IsConstructor(method model.Symbol, className string) bool

	// IsOverrideMatch returns true if childMethod overrides parentMethod.
	IsOverrideMatch(childMethod, parentMethod model.Symbol) bool

	// InferImplements infers implicit interface implementation relationships.
	// Go: method signature matching (duck typing). Other languages: return nil.
	InferImplements() []model.ResolvedRelation
}

// HeritageAware is an optional interface for helpers that need heritage data.
type HeritageAware interface {
	SetHeritage(heritage []model.RawHeritage)
}
