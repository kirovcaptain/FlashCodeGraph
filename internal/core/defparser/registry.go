package defparser

import (
	"github.com/kirovcaptain/FlashCodeGraph/internal/constants"
	"github.com/kirovcaptain/FlashCodeGraph/internal/core/parser/android"
)

// BuildManagers creates ORM and Schema managers based on detected frameworks.
func BuildManagers(frameworks []string) (orm *Manager, schema *Manager) {
	orm = NewManager()
	schema = NewManager()
	for _, fw := range frameworks {
		switch fw {
		case constants.Mybatis:
			orm.Register(&MybatisParser{})
		case constants.GraphQL:
			schema.Register(&GraphQLSchemaParser{})
		case constants.GRPC:
			schema.Register(&ProtoParser{})
		case constants.Android:
			orm.Register(&android.ManifestDefParser{})
			orm.Register(&android.NavigationDefParser{})
			orm.Register(&android.LayoutDefParser{})
		}
	}
	return orm, schema
}
