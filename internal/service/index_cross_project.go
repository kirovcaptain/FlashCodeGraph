package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/kirovcaptain/FlashCodeGraph/internal/config"
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/resolver"
	"github.com/kirovcaptain/FlashCodeGraph/internal/model"
	"github.com/kirovcaptain/FlashCodeGraph/internal/storage/crossindex"
)

func (indexer *Indexer) injectCrossProjectSymbols(ctx context.Context, scanCtx *scanContext, symbolTable *resolver.SymbolTable) (map[string]model.Node, error) {
	if indexer.crossIndex == nil || len(indexer.config.Dependencies.Projects) == 0 {
		return nil, nil
	}

	dependencies := toCrossIndexDeps(indexer.config.Dependencies.Projects)
	globalSymbols := indexer.crossIndex.GetDependencySymbols(ctx, dependencies)
	if len(globalSymbols) == 0 {
		return nil, nil
	}

	// Clean up previously injected cross-project nodes (safe no-op if none exist)
	if err := indexer.graphStore.DeleteNodesByFile(ctx, constants.FilePathCrossProject); err != nil {
		return nil, fmt.Errorf("clean old cross-project nodes: %w", err)
	}

	// Resolve which dependency each symbol came from by querying per dependency.
	type projectSource struct {
		path   string
		branch string
	}
	type symbolWithSource struct {
		symbol crossindex.GlobalSymbol
		source projectSource
	}
	var taggedSymbols []symbolWithSource
	for _, dep := range indexer.config.Dependencies.Projects {
		singleDep := []crossindex.Dependency{{Path: dep.Path, Branch: dep.Branch}}
		depSymbols := indexer.crossIndex.GetDependencySymbols(ctx, singleDep)
		source := projectSource{path: dep.Path, branch: dep.Branch}
		for _, globalSymbol := range depSymbols {
			taggedSymbols = append(taggedSymbols, symbolWithSource{symbol: globalSymbol, source: source})
		}
	}

	var symbols []model.Symbol
	preparedNodes := make(map[string]model.Node)

	for _, tagged := range taggedSymbols {
		globalSymbol := tagged.symbol
		sourceProject := tagged.source.path
		sourceBranch := tagged.source.branch

		// Class/Interface level symbol
		classID := "cross-project:" + globalSymbol.QualifiedName
		symbols = append(symbols, model.Symbol{
			ID:            classID,
			Name:          globalSymbol.Name,
			QualifiedName: globalSymbol.QualifiedName,
			Kind:          globalSymbol.Kind,
			ClassType:     globalSymbol.ClassType,
			FilePath:      constants.FilePathCrossProject,
		})
		preparedNodes[classID] = model.Node{
			ID:   classID,
			Kind: globalSymbol.Kind,
			Properties: map[string]any{
				"name":           globalSymbol.Name,
				"qualified_name": globalSymbol.QualifiedName,
				"class_type":     globalSymbol.ClassType,
				"file_path":      constants.FilePathCrossProject,
				"source_project": sourceProject,
				"source_branch":  sourceBranch,
			},
		}

		// Method level symbols — detect overloads for unique IDs
		methodNameCount := make(map[string]int)
		for _, method := range globalSymbol.Methods {
			methodNameCount[method.Name]++
		}

		for _, method := range globalSymbol.Methods {
			methodQualifiedName := globalSymbol.QualifiedName + "." + method.Name
			methodID := "cross-project:" + methodQualifiedName
			if methodNameCount[method.Name] > 1 && len(method.Params) > 0 {
				methodID = "cross-project:" + methodQualifiedName + "(" + strings.Join(method.Params, ",") + ")"
			}

			// Build params from GlobalMethod.Params (type names only)
			paramInfos := make([]model.ParamInfo, len(method.Params))
			for index, paramType := range method.Params {
				paramInfos[index] = model.ParamInfo{Name: fmt.Sprintf("p%d", index), Type: paramType}
			}

			symbols = append(symbols, model.Symbol{
				ID:            methodID,
				Name:          method.Name,
				QualifiedName: methodQualifiedName,
				Kind:          constants.KindFunction,
				FilePath:      constants.FilePathCrossProject,
				Params:        paramInfos,
			})
			preparedNodes[methodID] = model.Node{
				ID:   methodID,
				Kind: constants.KindFunction,
				Properties: map[string]any{
					"name":           method.Name,
					"qualified_name": methodQualifiedName,
					"file_path":      constants.FilePathCrossProject,
					"params":         marshalParams(paramInfos),
					"source_project": sourceProject,
					"source_branch":  sourceBranch,
					"is_getter":      method.IsGetter,
					"is_setter":      method.IsSetter,
				},
			}
		}
	}

	symbolTable.AddBatch(symbols)

	return preparedNodes, nil
}

// by matching HTTP method+path against Route nodes (same-repo) or CrossProjectIndex (cross-repo).
// Creates Function→Function CALLS edges so TraverseCallChain can traverse cross-service calls.
func (indexer *Indexer) matchConsumerToProvider(ctx context.Context, scanCtx *scanContext,
	remoteCalls []model.RawRemoteCall, pendingCalls []model.PendingRemoteCall,
	symbolTable *resolver.SymbolTable,
	allRoutes []model.Node, routeToHandler map[string]string) error {

	indexer.progress.EmitSub(PhaseWriting, SubMatchConsumerToProvider, "")

	if len(allRoutes) == 0 && indexer.crossIndex == nil {
		return nil
	}

	// Pre-bucket routes by HTTP method to reduce per-RemoteCall scan range.
	// Exclude consumer routes (e.g. feign) — only match against provider routes.
	routesByMethod := make(map[string][]model.Node)
	for _, route := range allRoutes {
		framework, _ := route.Properties["framework"].(string)
		if crossindex.DetermineRouteRole(framework) == crossindex.RoleConsumer {
			continue
		}
		method, _ := route.Properties["method"].(string)
		upperMethod := strings.ToUpper(method)
		routesByMethod[upperMethod] = append(routesByMethod[upperMethod], route)
	}

	dependencies := toCrossIndexDeps(indexer.config.Dependencies.Projects)

	var newNodes []model.Node
	var newEdges []model.Edge
	seenPlaceholders := make(map[string]bool)

	for _, remoteCall := range remoteCalls {
		if remoteCall.TargetURL == "" {
			continue
		}
		callerID := resolveHandlerFunction(symbolTable, remoteCall.CallerName, remoteCall.FilePath)
		if callerID == "" {
			continue
		}

		// 1. Match against local Route nodes (use method bucket to narrow scan)
		candidateRoutes := routesByMethod[strings.ToUpper(remoteCall.Method)]
		matched := resolver.FindMatchingRoutes(remoteCall.TargetURL, remoteCall.Method, candidateRoutes)
		if len(matched) > 0 {
			handlerID := routeToHandler[matched[0]]
			if handlerID != "" && handlerID != callerID {
				newEdges = append(newEdges, model.Edge{
					SourceID:   callerID,
					TargetID:   handlerID,
					Kind:       model.RelCalls,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"via_route":          remoteCall.Method + " " + remoteCall.TargetURL,
						"cross_service":      false,
						"consumer_interface": remoteCall.CallerName,
						"target_service":     remoteCall.TargetService,
						"confidence":         constants.ConfidenceViaRoute,
					},
				})
				scanCtx.result.RelationsByKind["CALLS_VIA_ROUTE"]++
			}
			continue
		}

		// 2. Match against CrossProjectIndex (only when dependencies configured)
		if len(dependencies) > 0 && indexer.crossIndex != nil {
			routeMatches := indexer.crossIndex.MatchRoute(ctx, remoteCall.Method, remoteCall.TargetURL, dependencies)
			if len(routeMatches) > 0 {
				match := routeMatches[0]
				placeholderID := match.Route.HandlerID
				if !seenPlaceholders[placeholderID] {
					seenPlaceholders[placeholderID] = true
					newNodes = append(newNodes, model.Node{
						ID:   placeholderID,
						Kind: constants.KindFunction,
						Properties: map[string]any{
							"name":           match.Route.HandlerName,
							"file_path":      constants.FilePathCrossService,
							"qualified_name": match.Route.HandlerName,
							"cross_service":  true,
							"target_project": match.ProjectPath,
							"target_branch":  match.Branch,
						},
					})
				}
				newEdges = append(newEdges, model.Edge{
					SourceID:   callerID,
					TargetID:   placeholderID,
					Kind:       model.RelCalls,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"via_route":          remoteCall.Method + " " + remoteCall.TargetURL,
						"cross_service":      true,
						"consumer_interface": remoteCall.CallerName,
						"target_service":     remoteCall.TargetService,
						"target_project":     match.ProjectPath,
						"target_branch":      match.Branch,
						"target_handler":     match.Route.HandlerName,
						"confidence":         constants.ConfidenceCrossService,
					},
				})
				scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]++
			}
		}
		// 3. No match → REMOTE_CALLS_EXT already created by writeRemoteCallEdges (Step 6)
	}

	// Step 3: Dubbo/gRPC qualified_name matching via PendingRemoteCall
	if len(pendingCalls) > 0 && len(dependencies) > 0 && indexer.crossIndex != nil {
		// Build ownerClass → callerFunctionID map from callRelations in symbolTable
		for _, pending := range pendingCalls {
			targetName := pending.FieldType
			// gRPC: FieldType may be "ServiceName/method" or full stub type, extract service name
			if pending.Protocol == "grpc" && strings.Contains(targetName, "/") {
				targetName = targetName[:strings.Index(targetName, "/")]
			}
			// Strip generic stub type suffix for gRPC (e.g. "PaymentServiceGrpc.PaymentServiceBlockingStub" → "PaymentService")
			if pending.Protocol == "grpc" && strings.Contains(targetName, "Grpc.") {
				grpcIndex := strings.Index(targetName, "Grpc.")
				if grpcIndex > 0 {
					targetName = targetName[:grpcIndex]
				}
			}

			// Try Route matching first (proto Routes or Go RegisterXxxServer Routes)
			routeMatches := indexer.crossIndex.MatchRouteByService(ctx, targetName, "grpc", dependencies)
			if len(routeMatches) > 0 {
				routeMatch := routeMatches[0]
				placeholderID := routeMatch.Route.HandlerID
				if placeholderID == "" {
					placeholderID = "cross:" + routeMatch.Route.HandlerName
				}
				callerMethods := symbolTable.FindMethodsByQualifiedName(pending.OwnerClass)
				for _, callerMethod := range callerMethods {
					if !seenPlaceholders[placeholderID] {
						seenPlaceholders[placeholderID] = true
						newNodes = append(newNodes, model.Node{
							ID:   placeholderID,
							Kind: constants.KindFunction,
							Properties: map[string]any{
								"name":           routeMatch.Route.HandlerName,
								"file_path":      constants.FilePathCrossService,
								"qualified_name": routeMatch.Route.HandlerName,
								"cross_service":  true,
								"target_project": routeMatch.ProjectPath,
								"target_branch":  routeMatch.Branch,
							},
						})
					}
					newEdges = append(newEdges, model.Edge{
						SourceID:   callerMethod.ID,
						TargetID:   placeholderID,
						Kind:       model.RelCalls,
						SourceKind: constants.KindFunction,
						Properties: map[string]any{
							"cross_service":  true,
							"target_service": targetName,
							"target_project": routeMatch.ProjectPath,
							"target_branch":  routeMatch.Branch,
							"target_handler": routeMatch.Route.HandlerName,
							"protocol":       pending.Protocol,
							"via_route":      routeMatch.Route.Path + "/" + routeMatch.Route.Method,
							"confidence":     constants.ConfidenceCrossService,
						},
					})
					scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]++
					break
				}
				continue
			}

			// Fallback: LookupSymbol by qualified name
			symbolMatches := indexer.crossIndex.LookupSymbol(ctx, targetName, dependencies)
			if len(symbolMatches) == 0 {
				continue
			}
			match := symbolMatches[0]

			// Find caller function: look up methods of the owning class that call this field's type
			callerMethods := symbolTable.FindMethodsByQualifiedName(pending.OwnerClass)
			for _, callerMethod := range callerMethods {
				callerID := callerMethod.ID
				placeholderID := match.Symbol.NodeID
				if !seenPlaceholders[placeholderID] {
					seenPlaceholders[placeholderID] = true
					newNodes = append(newNodes, model.Node{
						ID:   placeholderID,
						Kind: constants.KindFunction,
						Properties: map[string]any{
							"name":           match.Symbol.Name,
							"file_path":      constants.FilePathCrossService,
							"qualified_name": match.Symbol.QualifiedName,
							"cross_service":  true,
							"target_project": match.ProjectPath,
							"target_branch":  match.Branch,
						},
					})
				}
				newEdges = append(newEdges, model.Edge{
					SourceID:   callerID,
					TargetID:   placeholderID,
					Kind:       model.RelCalls,
					SourceKind: constants.KindFunction,
					Properties: map[string]any{
						"cross_service":      true,
						"consumer_interface": pending.FieldName,
						"target_service":     pending.FieldType,
						"target_project":     match.ProjectPath,
						"target_branch":      match.Branch,
						"target_handler":     match.Symbol.QualifiedName,
						"protocol":           pending.Protocol,
						"confidence":         constants.ConfidenceCrossService,
					},
				})
				scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]++
				break // one caller per pending is enough
			}
		}
	}

	if len(newNodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, newNodes); err != nil {
			return fmt.Errorf("create cross-service placeholder nodes: %w", err)
		}
	}
	if len(newEdges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, newEdges); err != nil {
			return fmt.Errorf("create cross-service CALLS edges: %w", err)
		}
	}
	indexer.dump.OnCrossServiceEdges(newNodes, newEdges)

	indexer.progress.EmitSub(PhaseWriting, SubMatchConsumerToProvider,
		fmt.Sprintf("%d via_route, %d cross_service",
			scanCtx.result.RelationsByKind["CALLS_VIA_ROUTE"],
			scanCtx.result.RelationsByKind["CALLS_CROSS_SERVICE"]))
	return nil
}

// toCrossIndexDeps converts config dependencies to crossindex.Dependency slice.
func toCrossIndexDeps(projects []config.DependencyProject) []crossindex.Dependency {
	dependencies := make([]crossindex.Dependency, len(projects))
	for i, project := range projects {
		dependencies[i] = crossindex.Dependency{Path: project.Path, Branch: project.Branch}
	}
	return dependencies
}

// writeCrossProjectIndex collects exported symbols and routes from the current project
// and registers them in the CrossProjectIndex for cross-service discovery.
func (indexer *Indexer) writeCrossProjectIndex(ctx context.Context, scanCtx *scanContext,
	symbolTable *resolver.SymbolTable,
	allRoutes []model.Node, routeToHandler map[string]string) error {

	if indexer.crossIndex == nil {
		return nil
	}

	indexer.progress.EmitSub(PhaseWriting, SubWriteCrossProjectIndex, "")

	var symbols []crossindex.GlobalSymbol
	var routes []crossindex.GlobalRoute

	// Collect exported classes/interfaces from symbolTable (skip functions, test files, unexported)
	for _, symbol := range symbolTable.All() {
		if !symbol.IsExported {
			continue
		}
		if isTestFilePath(symbol.FilePath) {
			continue
		}
		if symbol.Kind == constants.KindFunction {
			continue
		}

		globalSymbol := crossindex.GlobalSymbol{
			QualifiedName: symbol.QualifiedName,
			Name:          symbol.Name,
			Kind:          symbol.Kind,
			ClassType:     symbol.ClassType,
			NodeID:        symbol.ID,
			Annotations:   parseAnnotationNames(symbol.Annotations),
			FilePath:      symbol.FilePath,
		}

		// Collect methods of this class/interface
		methods := symbolTable.FindMethodsByQualifiedName(symbol.QualifiedName)
		for _, method := range methods {
			globalMethod := crossindex.GlobalMethod{
				Name:        method.Name,
				NodeID:      method.ID,
				Params:      extractParamTypeNames(method.Params),
				ReturnType:  firstReturnType(method.ReturnTypes),
				Annotations: parseAnnotationNames(method.Annotations),
				IsGetter:    method.IsGetter,
				IsSetter:    method.IsSetter,
			}
			globalMethod.RouteMethod, globalMethod.RoutePath = extractRouteFromAnnotations(method.Annotations)
			globalSymbol.Methods = append(globalSymbol.Methods, globalMethod)
		}

		symbols = append(symbols, globalSymbol)
	}

	// Collect routes with role (provider/consumer) using preloaded routeToHandler map
	for _, route := range allRoutes {
		framework, _ := route.Properties["framework"].(string)
		handlerMethod, _ := route.Properties["handler_method"].(string)
		routes = append(routes, crossindex.GlobalRoute{
			Method:      fmt.Sprint(route.Properties["method"]),
			Path:        fmt.Sprint(route.Properties["path_pattern"]),
			HandlerName: handlerMethod,
			HandlerID:   routeToHandler[route.ID],
			Framework:   framework,
			Role:        crossindex.DetermineRouteRole(framework),
		})
	}

	entry := crossindex.ProjectEntry{
		ProjectPath: scanCtx.absPath,
		Branch:      scanCtx.branch,
		Symbols:     symbols,
		Routes:      routes,
		UpdatedAt:   time.Now().Unix(),
	}
	if err := indexer.crossIndex.RegisterProject(ctx, entry); err != nil {
		return err
	}
	indexer.progress.EmitSub(PhaseWriting, SubWriteCrossProjectIndex,
		fmt.Sprintf("%d symbols, %d routes", len(symbols), len(routes)))
	return nil
}

func (indexer *Indexer) resolvePendingRemoteCalls(ctx context.Context, scanCtx *scanContext,
	parseResults []model.ParseResult, callRelations []model.ResolvedRelation,
	symbolTable *resolver.SymbolTable) error {

	var pendingCalls []model.PendingRemoteCall
	for _, parseResult := range parseResults {
		pendingCalls = append(pendingCalls, parseResult.PendingRemoteCalls...)
	}
	if len(pendingCalls) == 0 {
		return nil
	}

	indexer.progress.EmitSub(PhaseWriting, SubResolvePendingRemoteCalls, "")

	// Build ownerClass → []ResolvedRelation map for fast lookup
	relationsByOwner := make(map[string][]model.ResolvedRelation)
	for _, relation := range callRelations {
		sourceSymbol := symbolTable.FindByID(relation.SourceID)
		if sourceSymbol == nil {
			continue
		}
		lastDot := strings.LastIndex(sourceSymbol.QualifiedName, ".")
		if lastDot <= 0 {
			continue
		}
		ownerClass := sourceSymbol.QualifiedName[:lastDot]
		relationsByOwner[ownerClass] = append(relationsByOwner[ownerClass], relation)
	}

	var nodes []model.Node
	var edges []model.Edge
	seenExternalServices := make(map[string]bool)
	matchedCount := 0

	for _, pending := range pendingCalls {
		ownerRelations := relationsByOwner[pending.OwnerClass]
		for _, relation := range ownerRelations {
			targetSymbol := symbolTable.FindByID(relation.TargetID)
			if targetSymbol == nil {
				continue
			}
			if !strings.Contains(targetSymbol.QualifiedName, pending.FieldType) {
				continue
			}

			externalServiceID := "ext:" + pending.FieldType
			if !seenExternalServices[externalServiceID] {
				seenExternalServices[externalServiceID] = true
				nodes = append(nodes, model.Node{
					ID:   externalServiceID,
					Kind: constants.KindExternalService,
					Properties: map[string]any{
						"name":      pending.FieldType,
						"protocol":  pending.Protocol,
						"file_path": pending.FilePath,
					},
				})
			}

			edges = append(edges, model.Edge{
				SourceID:   relation.SourceID,
				TargetID:   externalServiceID,
				Kind:       model.RelRemoteCallsExt,
				SourceKind: constants.KindFunction,
				Properties: map[string]any{
					"target_service": pending.FieldType,
					"protocol":       pending.Protocol,
					"field_name":     pending.FieldName,
					"confidence":     constants.ConfidencePendingRemote,
				},
			})
			scanCtx.result.RelationsByKind["REMOTE_CALLS_EXT"]++
			matchedCount++
		}
	}

	if len(nodes) > 0 {
		if err := indexer.graphStore.CreateNodes(ctx, nodes); err != nil {
			return fmt.Errorf("create pending remote call nodes: %w", err)
		}
	}
	if len(edges) > 0 {
		if err := indexer.graphStore.CreateEdges(ctx, edges); err != nil {
			return fmt.Errorf("create pending remote call edges: %w", err)
		}
	}

	indexer.progress.EmitSub(PhaseWriting, SubResolvePendingRemoteCalls,
		fmt.Sprintf("%d pending, %d matched", len(pendingCalls), matchedCount))
	return nil
}

// clearParseResultsPhase1 nils fields consumed by writeSemanticNodes (excluding Symbols,
// which are still needed by resolveAndWriteRelations type inference).
