package resolver

import (
	"testing"

	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
)

func newEventDispatchResolver(symbols []model.Symbol, heritage []model.RawHeritage) *Resolver {
	table := NewSymbolTable()
	table.AddBatch(symbols)
	resolver := &Resolver{
		symbolTable:     table,
		heritage:        heritage,
		langHelpers:     make(map[string]LanguageHelper),
		heritageByChild: make(map[string][]model.RawHeritage),
	}
	for _, entry := range heritage {
		resolver.heritageByChild[entry.ChildName] = append(resolver.heritageByChild[entry.ChildName], entry)
	}
	return resolver
}

// U-1: Spring publishEvent → ApplicationListener
func TestResolveEventDispatches_ApplicationListener(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "listener1", Name: "onApplicationEvent", QualifiedName: "com.example.OrderListener.onApplicationEvent", Kind: "Function", FilePath: "OrderListener.java", StartLine: 5, EndLine: 15},
	}
	heritage := []model.RawHeritage{
		{ChildName: "OrderListener", ChildQualified: "com.example.OrderListener", ParentName: "ApplicationListener", Kind: "implements", TypeArgs: []model.TypeArg{{Name: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, heritage)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "listener1" {
		t.Errorf("expected targetID=listener1, got %s", relations[0].TargetID)
	}
	if relations[0].Confidence != 0.9 {
		t.Errorf("expected confidence=0.9, got %f", relations[0].Confidence)
	}
	if relations[0].ResolvedBy != "event_dispatch" {
		t.Errorf("expected resolved_by=event_dispatch, got %s", relations[0].ResolvedBy)
	}
	if relations[0].Metadata["event_type"] != "OrderEvent" {
		t.Errorf("expected event_type=OrderEvent, got %s", relations[0].Metadata["event_type"])
	}
}

// U-2: Spring publishEvent → @EventListener
func TestResolveEventDispatches_EventListenerAnnotation(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleOrderEvent", QualifiedName: "com.example.EventHandler.handleOrderEvent", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
	if relations[0].Confidence != 0.9 {
		t.Errorf("expected confidence=0.9, got %f", relations[0].Confidence)
	}
}

// U-3: Spring publishEvent → @TransactionalEventListener
func TestResolveEventDispatches_TransactionalEventListener(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "afterCommit", QualifiedName: "com.example.EventHandler.afterCommit", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"TransactionalEventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
}

// U-4: publishEvent variable arg — enrichArgTypes infers type
func TestResolveEventDispatches_VariableArg(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleOrderEvent", QualifiedName: "com.example.EventHandler.handleOrderEvent", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgExprs: []string{"event"}},
	}
	envs := map[string]*model.TypeEnv{
		"OrderService.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.OrderService.createOrder:event": {TypeName: "OrderEvent", Scope: "com.example.OrderService.createOrder"},
		}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
}

// U-5: One event, multiple listeners
func TestResolveEventDispatches_MultipleListeners(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleA", QualifiedName: "com.example.HandlerA.handleA", Kind: "Function", FilePath: "HandlerA.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "e", Type: "OrderEvent"}}},
		{ID: "handler2", Name: "handleB", QualifiedName: "com.example.HandlerB.handleB", Kind: "Function", FilePath: "HandlerB.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "e", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(relations))
	}
}

// U-6: Event inheritance — publish ChildEvent, listener monitors ParentEvent
func TestResolveEventDispatches_EventInheritance(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleParent", QualifiedName: "com.example.Handler.handleParent", Kind: "Function", FilePath: "Handler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "e", Type: "ParentEvent"}}},
	}
	heritage := []model.RawHeritage{
		{ChildName: "ChildEvent", ParentName: "ParentEvent", Kind: "extends"},
	}
	resolver := newEventDispatchResolver(symbols, heritage)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"ChildEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].Confidence != 0.85 {
		t.Errorf("expected confidence=0.85 for inherited match, got %f", relations[0].Confidence)
	}
}

// U-7: Guava post → @Subscribe
func TestResolveEventDispatches_GuavaPost(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "process", QualifiedName: "com.example.Service.process", Kind: "Function", FilePath: "Service.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handle", QualifiedName: "com.example.Subscriber.handle", Kind: "Function", FilePath: "Subscriber.java",
			Annotations: `[{"name":"Subscribe"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "post", CallerName: "com.example.Service.process", ReceiverExpr: "eventBus", FilePath: "Service.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{
		"Service.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Service.process:eventBus": {TypeName: "EventBus", Scope: "com.example.Service.process"},
		}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
}

// U-8: emit → on same ReceiverExpr
func TestResolveEventDispatches_EmitOnSameReceiver(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
	if relations[0].Metadata["event_type"] != "order:created" {
		t.Errorf("expected event_type=order:created, got %s", relations[0].Metadata["event_type"])
	}
}

// U-9: emit → on different ReceiverExpr — no match
func TestResolveEventDispatches_EmitOnDifferentReceiver(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "userEmitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "orderEmitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (different receiver), got %d", len(relations))
	}
}

// U-10: emit → on import alias resolution
func TestResolveEventDispatches_EmitOnAliasResolve(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "orderEmitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}, Imports: []model.RawImport{
			{SymbolName: "orderEmitter", Alias: "emitter", FilePath: "publisher.ts"},
		}},
		"consumer.ts": {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation (alias resolved), got %d", len(relations))
	}
}

// U-11: emit event name is not a string literal — no match
func TestResolveEventDispatches_EmitNonLiteral(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{"eventName"}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (non-literal event name), got %d", len(relations))
	}
}

// U-12: Multiple on registrations for same event
func TestResolveEventDispatches_MultipleOnRegistrations(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handlerA", Name: "handleA", QualifiedName: "src.consumer.handleA", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "handlerB", Name: "handleB", QualifiedName: "src.consumer.handleB", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 10},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 11, EndLine: 20},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 12, ArgExprs: []string{`"created"`, "handleA"}},
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 13, ArgExprs: []string{`"created"`, "handleB"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 2 {
		t.Fatalf("expected 2 relations, got %d", len(relations))
	}
}

// U-13: post receiver is not EventBus — no match
func TestResolveEventDispatches_PostNonEventBus(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "process", QualifiedName: "com.example.Service.process", Kind: "Function", FilePath: "Service.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handle", QualifiedName: "com.example.Subscriber.handle", Kind: "Function", FilePath: "Subscriber.java",
			Annotations: `[{"name":"Subscribe"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "post", CallerName: "com.example.Service.process", ReceiverExpr: "httpClient", FilePath: "Service.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{
		"Service.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Service.process:httpClient": {TypeName: "HttpClient", Scope: "com.example.Service.process"},
		}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (non-EventBus receiver), got %d", len(relations))
	}
}

// U-14: publishEvent with empty ArgType — no panic, no match
func TestResolveEventDispatches_EmptyArgType(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleOrderEvent", QualifiedName: "com.example.EventHandler.handleOrderEvent", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (empty arg type), got %d", len(relations))
	}
}

// U-15: emit → on handler is named function
func TestResolveEventDispatches_OnNamedHandler(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
}

// U-16: emit → on handler is this.xxx.bind(this)
func TestResolveEventDispatches_OnThisBindHandler(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.OrderService.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.OrderService.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "on", CallerName: "src.consumer.OrderService.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "this.handleOrder.bind(this)"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "handler1" {
		t.Errorf("expected targetID=handler1, got %s", relations[0].TargetID)
	}
}

// U-17: emit → on handler is lambda (pre-resolved RawCall)
func TestResolveEventDispatches_OnLambdaHandler(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "lambda1", Name: "lambda$1", QualifiedName: "src.consumer.register.lambda$1", Kind: "Function", FilePath: "consumer.ts", StartLine: 10, EndLine: 10, IsLambda: true},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		// Normal on call (ArgExprs[1] is empty because arrow_function)
		{CalledName: "on", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, ""}},
		// Pre-resolved lambda call
		{CalledName: "src.consumer.register.lambda$1", CallerName: "src.consumer.register", FilePath: "consumer.ts", Line: 10, IsPreResolved: true, LambdaOwnerMethod: "on", LambdaOwnerReceiver: "emitter"},
		// Emit call
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].TargetID != "lambda1" {
		t.Errorf("expected targetID=lambda1, got %s", relations[0].TargetID)
	}
}

// U-18: post receiver is EventBus (positive)
func TestResolveEventDispatches_PostEventBusPositive(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "process", QualifiedName: "com.example.Service.process", Kind: "Function", FilePath: "Service.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handle", QualifiedName: "com.example.Subscriber.handle", Kind: "Function", FilePath: "Subscriber.java",
			Annotations: `[{"name":"Subscribe"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "post", CallerName: "com.example.Service.process", ReceiverExpr: "eventBus", FilePath: "Service.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{
		"Service.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.Service.process:eventBus": {TypeName: "com.google.common.eventbus.EventBus", Scope: "com.example.Service.process"},
		}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
}

// U-19: once registration
func TestResolveEventDispatches_OnceRegistration(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "once", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
}

// U-20: addListener registration
func TestResolveEventDispatches_AddListenerRegistration(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "emitter1", Name: "publish", QualifiedName: "src.publisher.publish", Kind: "Function", FilePath: "publisher.ts", StartLine: 1, EndLine: 10},
		{ID: "handler1", Name: "handleOrder", QualifiedName: "src.consumer.handleOrder", Kind: "Function", FilePath: "consumer.ts", StartLine: 1, EndLine: 5},
		{ID: "register1", Name: "register", QualifiedName: "src.consumer.register", Kind: "Function", FilePath: "consumer.ts", StartLine: 6, EndLine: 15},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "addListener", CallerName: "src.consumer.register", ReceiverExpr: "emitter", FilePath: "consumer.ts", Line: 10, ArgExprs: []string{`"order:created"`, "handleOrder"}},
		{CalledName: "emit", CallerName: "src.publisher.publish", ReceiverExpr: "emitter", FilePath: "publisher.ts", Line: 5, ArgExprs: []string{`"order:created"`}},
	}
	envs := map[string]*model.TypeEnv{
		"publisher.ts": {Bindings: map[string]*model.TypeInfo{}},
		"consumer.ts":  {Bindings: map[string]*model.TypeInfo{}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
}

// U-21: publishEvent receiver is not Spring type — no match
func TestResolveEventDispatches_PublishEventNonSpringReceiver(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleOrderEvent", QualifiedName: "com.example.EventHandler.handleOrderEvent", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", ReceiverExpr: "customService", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderEvent"}},
	}
	envs := map[string]*model.TypeEnv{
		"OrderService.java": {Bindings: map[string]*model.TypeInfo{
			"com.example.OrderService.createOrder:customService": {TypeName: "CustomService", Scope: "com.example.OrderService.createOrder"},
		}},
	}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 0 {
		t.Fatalf("expected 0 relations (non-Spring receiver), got %d", len(relations))
	}
}

// U-22: event_type metadata is set correctly
func TestResolveEventDispatches_EventTypeMetadata(t *testing.T) {
	symbols := []model.Symbol{
		{ID: "publisher1", Name: "createOrder", QualifiedName: "com.example.OrderService.createOrder", Kind: "Function", FilePath: "OrderService.java", StartLine: 10, EndLine: 20},
		{ID: "handler1", Name: "handleOrderEvent", QualifiedName: "com.example.EventHandler.handleOrderEvent", Kind: "Function", FilePath: "EventHandler.java",
			Annotations: `[{"name":"EventListener"}]`, Params: []model.ParamInfo{{Name: "event", Type: "OrderCreatedEvent"}}},
	}
	resolver := newEventDispatchResolver(symbols, nil)

	calls := []model.RawCall{
		{CalledName: "publishEvent", CallerName: "com.example.OrderService.createOrder", FilePath: "OrderService.java", Line: 15, ArgTypes: []string{"OrderCreatedEvent"}},
	}
	envs := map[string]*model.TypeEnv{"OrderService.java": {Bindings: map[string]*model.TypeInfo{}}}

	relations := resolver.ResolveEventDispatches(calls, envs)
	if len(relations) != 1 {
		t.Fatalf("expected 1 relation, got %d", len(relations))
	}
	if relations[0].Metadata["event_type"] != "OrderCreatedEvent" {
		t.Errorf("expected event_type=OrderCreatedEvent, got %s", relations[0].Metadata["event_type"])
	}
}
