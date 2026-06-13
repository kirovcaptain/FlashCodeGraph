package resolver

import (
	"strings"

	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

// eventListener represents a discovered event consumer with its target method.
type eventListener struct {
	eventType string // event class name (Java) or event name string (TS)
	targetID  string // Symbol ID of the listener method
	framework string // "spring" or "guava" — used to match only with corresponding publisher
}

// emitterListener represents a TS EventEmitter on/once/addListener registration.
type emitterListener struct {
	eventName    string // trimmed event name (without quotes)
	receiverExpr string // receiver expression of the on/once/addListener call
	targetID     string // Symbol ID of the handler method or lambda
}

// ResolveEventDispatches matches event publishers to event listeners and produces CALLS edges.
// Must be called after ResolveCalls (Step 4) and DetectOverridesAndDispatches (Step 6).
func (resolver *Resolver) ResolveEventDispatches(
	allCalls []model.RawCall,
	envs map[string]*model.TypeEnv,
) []model.ResolvedRelation {
	// Phase 1: Collect event listeners
	javaListeners := resolver.collectJavaEventListeners()
	emitterListeners := resolver.collectEmitterListeners(allCalls)

	// Phase 2: Collect event publishers and match
	var relations []model.ResolvedRelation

	for _, call := range allCalls {
		if call.IsPreResolved {
			continue
		}
		switch {
		case call.CalledName == "publishEvent":
			relations = append(relations, resolver.matchPublishEvent(call, envs, javaListeners)...)
		case call.CalledName == "post":
			relations = append(relations, resolver.matchGuavaPost(call, envs, javaListeners)...)
		case call.CalledName == "emit":
			relations = append(relations, resolver.matchEmit(call, envs, emitterListeners)...)
		}
	}

	return relations
}

// collectJavaEventListeners scans Heritage and SymbolTable for Java event listeners.
func (resolver *Resolver) collectJavaEventListeners() []eventListener {
	var listeners []eventListener

	// 1. ApplicationListener<X> via Heritage
	for _, heritage := range resolver.heritage {
		if heritage.ParentName != "ApplicationListener" || heritage.Kind != "implements" {
			continue
		}
		if len(heritage.TypeArgs) == 0 {
			continue
		}
		eventTypeName := heritage.TypeArgs[0].Name
		// Find onApplicationEvent method in the child class
		childMethods := resolver.symbolTable.FindByName("onApplicationEvent")
		for _, method := range childMethods {
			if method.Kind != constants.KindFunction {
				continue
			}
			if strings.Contains(method.QualifiedName, heritage.ChildName+".") {
				listeners = append(listeners, eventListener{
					eventType: eventTypeName,
					targetID:  method.ID,
					framework: "spring",
				})
			}
		}
	}

	// 2. @EventListener / @TransactionalEventListener / @Subscribe annotated methods
	for _, symbol := range resolver.symbolTable.All() {
		if symbol.Kind != constants.KindFunction || len(symbol.Annotations) == 0 {
			continue
		}
		for _, annotation := range symbol.Annotations {
			if annotation.Name != "EventListener" && annotation.Name != "TransactionalEventListener" && annotation.Name != "Subscribe" {
				continue
			}
			// Event type from first parameter's type
			if len(symbol.Params) > 0 && symbol.Params[0].Type != "" {
				listenerFramework := "spring"
				if annotation.Name == "Subscribe" {
					listenerFramework = "guava"
				}
				listeners = append(listeners, eventListener{
					eventType: symbol.Params[0].Type,
					targetID:  symbol.ID,
					framework: listenerFramework,
				})
			}
		}
	}

	return listeners
}

// collectEmitterListeners scans RawCalls for on/once/addListener registrations.
func (resolver *Resolver) collectEmitterListeners(allCalls []model.RawCall) []emitterListener {
	var listeners []emitterListener

	// Build index of pre-resolved lambda calls by CallerName+Line for lambda handler lookup
	type lambdaKey struct {
		callerName string
		line       int
	}
	lambdaByOwnerCall := make(map[lambdaKey]string) // key → lambda qualified name
	for _, call := range allCalls {
		if call.IsPreResolved && isEventListenerMethod(call.LambdaOwnerMethod) {
			lambdaByOwnerCall[lambdaKey{call.CallerName, call.Line}] = call.CalledName
		}
	}

	for _, call := range allCalls {
		if call.IsPreResolved || !isEventListenerMethod(call.CalledName) {
			continue
		}
		if len(call.ArgExprs) == 0 {
			continue
		}
		eventNameRaw := call.ArgExprs[0]
		if !isStringLiteral(eventNameRaw) {
			continue
		}
		eventName := trimQuotes(eventNameRaw)

		// Determine handler target ID
		var targetID string
		if len(call.ArgExprs) >= 2 && call.ArgExprs[1] != "" {
			targetID = resolver.resolveEmitterHandler(call.ArgExprs[1], call)
		}
		// Check for lambda handler (ArgExprs[1] == "" when arrow_function)
		if targetID == "" {
			if lambdaQualifiedName, exists := lambdaByOwnerCall[lambdaKey{call.CallerName, call.Line}]; exists {
				symbols := resolver.symbolTable.FindByQualifiedName(lambdaQualifiedName)
				if len(symbols) > 0 {
					targetID = symbols[0].ID
				}
			}
		}
		if targetID == "" {
			continue
		}

		listeners = append(listeners, emitterListener{
			eventName:    eventName,
			receiverExpr: call.ReceiverExpr,
			targetID:     targetID,
		})
	}

	return listeners
}

// resolveEmitterHandler resolves the handler expression from on/once/addListener's second argument.
func (resolver *Resolver) resolveEmitterHandler(handlerExpr string, call model.RawCall) string {
	// Pattern: this.xxx.bind(this)
	if strings.HasPrefix(handlerExpr, "this.") && strings.HasSuffix(handlerExpr, ".bind(this)") {
		methodName := handlerExpr[5 : len(handlerExpr)-len(".bind(this)")]
		return resolver.findMethodInCallerClass(methodName, call.CallerName)
	}
	// Pattern: this.xxx (no further dots)
	if strings.HasPrefix(handlerExpr, "this.") && !strings.Contains(handlerExpr[5:], ".") {
		methodName := handlerExpr[5:]
		return resolver.findMethodInCallerClass(methodName, call.CallerName)
	}
	// Simple identifier
	if !strings.Contains(handlerExpr, ".") && !strings.Contains(handlerExpr, "(") && !strings.Contains(handlerExpr, "=>") {
		candidates := resolver.symbolTable.FindByName(handlerExpr)
		for _, candidate := range candidates {
			if candidate.Kind == constants.KindFunction && candidate.FilePath == call.FilePath {
				return candidate.ID
			}
		}
		// Fallback: any function with that name
		for _, candidate := range candidates {
			if candidate.Kind == constants.KindFunction {
				return candidate.ID
			}
		}
	}
	return ""
}

// findMethodInCallerClass finds a method by name in the same class as the caller.
func (resolver *Resolver) findMethodInCallerClass(methodName, callerName string) string {
	// Extract class qualified name from callerName (e.g. "pkg.Class.method" → "pkg.Class")
	lastDot := strings.LastIndex(callerName, ".")
	if lastDot < 0 {
		return ""
	}
	classQualifiedName := callerName[:lastDot]
	expectedPrefix := classQualifiedName + "." + methodName

	candidates := resolver.symbolTable.FindByName(methodName)
	for _, candidate := range candidates {
		if candidate.Kind == constants.KindFunction && candidate.QualifiedName == expectedPrefix {
			return candidate.ID
		}
	}
	return ""
}

// matchPublishEvent matches a publishEvent call to Java event listeners.
func (resolver *Resolver) matchPublishEvent(call model.RawCall, envs map[string]*model.TypeEnv, listeners []eventListener) []model.ResolvedRelation {
	eventTypeName := resolver.resolvePublishEventType(call, envs)
	if eventTypeName == "" {
		return nil
	}

	// Check receiver type — should be ApplicationEventPublisher or ApplicationContext
	// If receiver type is known and doesn't match, skip (risk 7.1)
	if call.ReceiverExpr != "" {
		receiverType := resolver.lookupSimpleReceiverType(call, envs)
		if receiverType != "" && !isSpringPublisherType(receiverType) {
			return nil
		}
	}

	sourceID := resolver.findCallerID(call)
	if strings.HasPrefix(sourceID, "caller:") {
		return nil
	}

	return resolver.buildEventRelations(sourceID, eventTypeName, listeners, call.Line, "spring")
}

// matchGuavaPost matches an eventBus.post call to @Subscribe listeners.
func (resolver *Resolver) matchGuavaPost(call model.RawCall, envs map[string]*model.TypeEnv, listeners []eventListener) []model.ResolvedRelation {
	// Check receiver type must be EventBus
	receiverType := resolver.lookupSimpleReceiverType(call, envs)
	if receiverType != "EventBus" && !strings.HasSuffix(receiverType, ".EventBus") {
		return nil
	}

	eventTypeName := resolver.resolvePublishEventType(call, envs)
	if eventTypeName == "" {
		return nil
	}

	sourceID := resolver.findCallerID(call)
	if strings.HasPrefix(sourceID, "caller:") {
		return nil
	}

	return resolver.buildEventRelations(sourceID, eventTypeName, listeners, call.Line, "guava")
}

// matchEmit matches an emitter.emit call to emitter listeners.
func (resolver *Resolver) matchEmit(call model.RawCall, envs map[string]*model.TypeEnv, emitterListeners []emitterListener) []model.ResolvedRelation {
	if len(call.ArgExprs) == 0 {
		return nil
	}
	eventNameRaw := call.ArgExprs[0]
	if !isStringLiteral(eventNameRaw) {
		return nil
	}
	eventName := trimQuotes(eventNameRaw)

	sourceID := resolver.findCallerID(call)
	if strings.HasPrefix(sourceID, "caller:") {
		return nil
	}

	// Resolve emit receiver alias
	env := envs[call.FilePath]
	emitReceiver := resolveReceiverAlias(call.ReceiverExpr, env)

	var relations []model.ResolvedRelation
	for _, listener := range emitterListeners {
		if listener.eventName != eventName {
			continue
		}
		listenerReceiver := resolveReceiverAlias(listener.receiverExpr, env)
		if emitReceiver != listenerReceiver {
			continue
		}
		relations = append(relations, model.ResolvedRelation{
			SourceID:   sourceID,
			TargetID:   listener.targetID,
			Kind:       model.RelCalls,
			SourceKind: constants.KindFunction,
			Confidence: 0.9,
			ResolvedBy: "event_dispatch",
			Line:       call.Line,
			Metadata:   map[string]string{"event_type": eventName},
		})
	}
	return relations
}

// buildEventRelations creates CALLS edges from a publisher to matching Java listeners.
func (resolver *Resolver) buildEventRelations(sourceID, eventTypeName string, listeners []eventListener, line int, publisherFramework string) []model.ResolvedRelation {
	var relations []model.ResolvedRelation
	for _, listener := range listeners {
		if listener.framework != publisherFramework {
			continue
		}
		isExact := listener.eventType == eventTypeName
		isInherited := !isExact && resolver.isEventTypeMatch(eventTypeName, listener.eventType)
		if !isExact && !isInherited {
			continue
		}
		confidence := 0.9
		if isInherited {
			confidence = 0.85
		}
		relations = append(relations, model.ResolvedRelation{
			SourceID:   sourceID,
			TargetID:   listener.targetID,
			Kind:       model.RelCalls,
			SourceKind: constants.KindFunction,
			Confidence: confidence,
			ResolvedBy: "event_dispatch",
			Line:       line,
			Metadata:   map[string]string{"event_type": eventTypeName},
		})
	}
	return relations
}

// resolvePublishEventType extracts the event type from a publishEvent/post call.
func (resolver *Resolver) resolvePublishEventType(call model.RawCall, envs map[string]*model.TypeEnv) string {
	// First check ArgTypes (filled by inferArgType for "new OrderEvent()")
	if len(call.ArgTypes) > 0 && call.ArgTypes[0] != "" {
		return call.ArgTypes[0]
	}
	// Fallback: infer from ArgExprs using TypeEnv (simple variable lookup without langHelper)
	if len(call.ArgExprs) > 0 && call.ArgExprs[0] != "" {
		expr := call.ArgExprs[0]
		// Simple variable — lookup in TypeEnv
		if !strings.Contains(expr, ".") && !strings.Contains(expr, "(") {
			env := envs[call.FilePath]
			if env != nil {
				scope := effectiveCallerScope(call)
				if info := lookupBindingWithScopeChain(env, scope, expr); info != nil {
					return extractSimpleType(info.TypeName)
				}
			}
		}
		// Uppercase identifier — class name convention (e.g. new OrderEvent() without ArgTypes)
		if len(expr) > 0 && expr[0] >= 'A' && expr[0] <= 'Z' && !strings.Contains(expr, " ") {
			return expr
		}
	}
	return ""
}

// lookupSimpleReceiverType resolves the type of a simple receiver expression via TypeEnv scope chain.
// Unlike lookupReceiverType, this does not require a langHelper and does not handle chained receivers.
func (resolver *Resolver) lookupSimpleReceiverType(call model.RawCall, envs map[string]*model.TypeEnv) string {
	if call.ReceiverExpr == "" {
		return ""
	}
	env := envs[call.FilePath]
	if env == nil {
		return ""
	}
	scope := effectiveCallerScope(call)
	if info := lookupBindingWithScopeChain(env, scope, call.ReceiverExpr); info != nil {
		return extractSimpleType(info.TypeName)
	}
	return ""
}

// isEventTypeMatch checks if publishType is a subclass of listenerType via EXTENDS chain.
func (resolver *Resolver) isEventTypeMatch(publishType, listenerType string) bool {
	visited := map[string]bool{}
	queue := []string{publishType}
	for depth := 0; len(queue) > 0 && depth < 5; depth++ {
		nextQueue := []string{}
		for _, current := range queue {
			if visited[current] {
				continue
			}
			visited[current] = true
			for _, heritage := range resolver.heritage {
				if heritage.Kind != "extends" {
					continue
				}
				if heritage.ChildName != current && heritage.ChildQualified != current {
					continue
				}
				if heritage.ParentName == listenerType || heritage.ParentQualified == listenerType {
					return true
				}
				nextQueue = append(nextQueue, heritage.ParentName)
			}
		}
		queue = nextQueue
	}
	return false
}

// resolveReceiverAlias resolves import aliases for receiver expressions.
func resolveReceiverAlias(receiverExpr string, env *model.TypeEnv) string {
	if env == nil || receiverExpr == "" {
		return receiverExpr
	}
	for _, imp := range env.Imports {
		if imp.Alias == receiverExpr && imp.SymbolName != "" {
			return imp.SymbolName
		}
	}
	return receiverExpr
}

// isEventListenerMethod returns true if the method name is an EventEmitter listener registration.
func isEventListenerMethod(name string) bool {
	return name == "on" || name == "once" || name == "addListener"
}

// isStringLiteral checks if an expression is a string literal (starts with quote).
func isStringLiteral(expr string) bool {
	return strings.HasPrefix(expr, "\"") || strings.HasPrefix(expr, "'") || strings.HasPrefix(expr, "`")
}

// trimQuotes removes surrounding quotes from a string literal.
func trimQuotes(expr string) string {
	return strings.Trim(expr, "\"'`")
}

// isSpringPublisherType checks if a type is a Spring event publisher.
func isSpringPublisherType(typeName string) bool {
	return typeName == "ApplicationEventPublisher" || typeName == "ApplicationContext" ||
		strings.HasSuffix(typeName, ".ApplicationEventPublisher") ||
		strings.HasSuffix(typeName, ".ApplicationContext")
}
