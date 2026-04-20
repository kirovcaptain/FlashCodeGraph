package defparser

import "github.com/kirovcaptain/FlashCodeGraph/internal/constants"

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
		}
	}
	return orm, schema
}
