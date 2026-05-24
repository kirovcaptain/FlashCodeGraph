// Package annotation defines valuable annotation configurations per framework.
package annotation

import "sort"

// AnnotationDef defines a valuable annotation to be indexed.
type AnnotationDef struct {
	Name      string // annotation name without @, e.g. "Service"
	Category  string // layer/scheduled/transaction/async/cache/event_listener/security/config/lifecycle/test/query/rpc/graphql/custom
	Layer     string // only for category=layer: controller/service/repository/component/config/model
	Framework string // source framework: spring/mybatis/dubbo/nestjs/custom/_common
	EntryType string // entry point type for analyze classification: scheduled_task/event_handler/rpc_handler (empty = not an entry point)
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
		{Name: "Transactional", Category: "transaction", Framework: "spring"},
		{Name: "Async", Category: "async", Framework: "spring"},
		{Name: "Scheduled", Category: "scheduled", EntryType: "scheduled_task", Framework: "spring"},
		{Name: "Cacheable", Category: "cache", Framework: "spring"},
		{Name: "CacheEvict", Category: "cache", Framework: "spring"},
		{Name: "EventListener", Category: "event_listener", EntryType: "event_handler", Framework: "spring"},
		{Name: "TransactionalEventListener", Category: "event_listener", EntryType: "event_handler", Framework: "spring"},
		{Name: "KafkaListener", Category: "event_listener", EntryType: "event_handler", Framework: "spring"},
		{Name: "RabbitListener", Category: "event_listener", EntryType: "event_handler", Framework: "spring"},
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
		{Name: "DubboService", Category: "rpc", EntryType: "rpc_handler", Framework: "dubbo"},
		{Name: "GrpcService", Category: "rpc", EntryType: "rpc_handler", Framework: "dubbo"},
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
		{Name: "Cron", Category: "scheduled", EntryType: "scheduled_task", Framework: "nestjs"},
	},
	"fastapi": {
		{Name: "Depends", Category: "dependency", Framework: "fastapi"},
	},
	"xxl-job": {
		{Name: "XxlJob", Category: "scheduled", EntryType: "scheduled_task", Framework: "xxl-job"},
	},
	"rocketmq": {
		{Name: "RocketMQMessageListener", Category: "event_listener", EntryType: "event_handler", Framework: "rocketmq"},
	},
	"guava": {
		{Name: "Subscribe", Category: "event_listener", EntryType: "event_handler", Framework: "guava"},
	},
	"swagger2": {
		{Name: "ApiOperation", Category: "doc", Framework: "swagger2"},
	},
	"swagger3": {
		{Name: "Operation", Category: "doc", Framework: "swagger3"},
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

// entryTypeByName maps annotation name → EntryType for quick lookup.
var entryTypeByName map[string]string

func init() {
	entryTypeByName = make(map[string]string)
	for _, defs := range DefaultAnnotations {
		for _, def := range defs {
			if def.EntryType != "" {
				entryTypeByName[def.Name] = def.EntryType
			}
		}
	}
}

// LookupEntryType returns the EntryType for an annotation name, or "" if not an entry point.
func LookupEntryType(annotationName string) string {
	return entryTypeByName[annotationName]
}

// ListCategories returns all unique category values from DefaultAnnotations, sorted.
func ListCategories() []string {
	seen := make(map[string]bool)
	for _, defs := range DefaultAnnotations {
		for _, def := range defs {
			if def.Category != "" {
				seen[def.Category] = true
			}
		}
	}
	categories := make([]string, 0, len(seen))
	for c := range seen {
		categories = append(categories, c)
	}
	sort.Strings(categories)
	return categories
}
