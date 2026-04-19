// Package annotation defines valuable annotation configurations per framework.
package annotation

// AnnotationDef defines a valuable annotation to be indexed.
type AnnotationDef struct {
	Name      string // annotation name without @, e.g. "Service"
	Category  string // layer/behavior/security/config/lifecycle/test/query/rpc/graphql/custom
	Layer     string // only for category=layer: controller/service/repository/component/config/model
	Framework string // source framework: spring/mybatis/dubbo/nestjs/custom/_common
}

// DefaultAnnotations maps framework name → valuable annotations.
var DefaultAnnotations = map[string][]AnnotationDef{
	"spring": {
		{Name: "RestController", Category: "layer", Layer: "controller", Framework: "spring"},
		{Name: "Controller", Category: "layer", Layer: "controller", Framework: "spring"},
		{Name: "Service", Category: "layer", Layer: "service", Framework: "spring"},
		{Name: "Repository", Category: "layer", Layer: "repository", Framework: "spring"},
		{Name: "Component", Category: "layer", Layer: "component", Framework: "spring"},
		{Name: "Configuration", Category: "layer", Layer: "config", Framework: "spring"},
		{Name: "Entity", Category: "layer", Layer: "model", Framework: "spring"},
		{Name: "Table", Category: "layer", Layer: "model", Framework: "spring"},
		{Name: "MappedSuperclass", Category: "layer", Layer: "model", Framework: "spring"},
		{Name: "Transactional", Category: "behavior", Framework: "spring"},
		{Name: "Async", Category: "behavior", Framework: "spring"},
		{Name: "Scheduled", Category: "behavior", Framework: "spring"},
		{Name: "Cacheable", Category: "behavior", Framework: "spring"},
		{Name: "CacheEvict", Category: "behavior", Framework: "spring"},
		{Name: "EventListener", Category: "behavior", Framework: "spring"},
		{Name: "PostConstruct", Category: "lifecycle", Framework: "spring"},
		{Name: "PreDestroy", Category: "lifecycle", Framework: "spring"},
		{Name: "PreAuthorize", Category: "security", Framework: "spring"},
		{Name: "Secured", Category: "security", Framework: "spring"},
		{Name: "RolesAllowed", Category: "security", Framework: "spring"},
		{Name: "Value", Category: "config", Framework: "spring"},
		{Name: "ConditionalOnProperty", Category: "config", Framework: "spring"},
		{Name: "Profile", Category: "config", Framework: "spring"},
	},
	"mybatis": {
		{Name: "Mapper", Category: "layer", Layer: "repository", Framework: "mybatis"},
		{Name: "Select", Category: "query", Framework: "mybatis"},
		{Name: "Insert", Category: "query", Framework: "mybatis"},
		{Name: "Update", Category: "query", Framework: "mybatis"},
		{Name: "Delete", Category: "query", Framework: "mybatis"},
	},
	"hibernate": {
		{Name: "Entity", Category: "layer", Layer: "model", Framework: "hibernate"},
	},
	"dubbo": {
		{Name: "DubboService", Category: "rpc", Framework: "dubbo"},
		{Name: "DubboReference", Category: "rpc", Framework: "dubbo"},
	},
	"graphql": {
		{Name: "QueryMapping", Category: "graphql", Framework: "graphql"},
		{Name: "MutationMapping", Category: "graphql", Framework: "graphql"},
		{Name: "SubscriptionMapping", Category: "graphql", Framework: "graphql"},
	},
	"nestjs": {
		{Name: "Controller", Category: "layer", Layer: "controller", Framework: "nestjs"},
		{Name: "Injectable", Category: "layer", Layer: "service", Framework: "nestjs"},
		{Name: "Module", Category: "layer", Layer: "config", Framework: "nestjs"},
		{Name: "Guard", Category: "security", Framework: "nestjs"},
		{Name: "UseGuards", Category: "security", Framework: "nestjs"},
		{Name: "Cron", Category: "behavior", Framework: "nestjs"},
	},
	"fastapi": {
		{Name: "Depends", Category: "behavior", Framework: "fastapi"},
	},
	"_test": {
		{Name: "Test", Category: "test", Framework: "junit"},
		{Name: "SpringBootTest", Category: "test", Framework: "spring"},
		{Name: "MockBean", Category: "test", Framework: "spring"},
	},
	"_always": {
		{Name: "Deprecated", Category: "lifecycle", Framework: "_common"},
	},
}
