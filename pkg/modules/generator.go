package modules

import (
	"encoding/json"

	"github.com/iancoleman/orderedmap"
	"github.com/invopop/jsonschema"
	pgs "github.com/lyft/protoc-gen-star/v2"
	"github.com/pubg/protoc-gen-jsonschema/pkg/proto"
	"github.com/samber/lo"
	"google.golang.org/protobuf/types/known/anypb"
)

func buildFromMessage(message pgs.Message, mo *proto.MessageOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Type = "object"
	schema.Title = message.Name().UpperCamelCase().String()
	schema.Description = message.SourceCodeInfo().LeadingComments()
	schema.Properties = orderedmap.New()

	fillSchemaByObjectKeywords(schema, mo.GetObject())

	for _, field := range message.Fields() {
		// Skip OneOf Block field
		//if field.OneOf() != nil {
		//	continue
		//}

		propName := toPropertyName(field.Name())
		schema.Properties.Set(propName, &jsonschema.Schema{Ref: toDef(field)})

		// If field is not a member of oneOf
		if !field.InRealOneOf() && !field.HasOptionalKeyword() {
			schema.Required = append(schema.Required, propName)
		}
	}

	// Convert Protobuf OneOfs to JSONSchema keywords
	for _, oneOf := range message.OneOfs() {
		propertyNames := lo.Map[pgs.Field, string](oneOf.Fields(), func(item pgs.Field, _ int) string {
			return toPropertyName(item.Name())
		})
		oneOfSchemas := lo.Map[string, *jsonschema.Schema](propertyNames, func(item string, _ int) *jsonschema.Schema {
			return &jsonschema.Schema{Required: []string{item}}
		})

		negativeSchema := &jsonschema.Schema{Not: &jsonschema.Schema{AnyOf: make([]*jsonschema.Schema, len(oneOfSchemas))}}
		copy(negativeSchema.Not.AnyOf, oneOfSchemas)

		combinedSchemas := append(oneOfSchemas, negativeSchema)
		schema.AllOf = append(schema.AllOf, &jsonschema.Schema{OneOf: combinedSchemas})
	}
	return schema
}

func buildFromMessageField(field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescription(field, fo)

	if field.Type().IsRepeated() {
		schema.Ref = toDef(field.Type().Element().Embed())
	} else {
		schema.Ref = toDef(field.Type().Embed())
	}

	if field.Type().IsRepeated() {
		return wrapSchemaInArray(schema, field, fo)
	} else {
		return schema
	}
}

// TODO: 미완성
func buildFromMapField(field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescription(field, fo)
	schema.Type = "object"

	valueSchema := &jsonschema.Schema{}
	value := field.Type().Element()
	protoType := value.ProtoType()
	if protoType == pgs.MessageT {
		valueSchema.Ref = toDef(value.Embed())
	} else if protoType.IsNumeric() {
		valueSchema.Type = "number"
	} else if protoType == pgs.BoolT {
		valueSchema.Type = "boolean"
	} else if protoType == pgs.EnumT {
		valueSchema.Ref = toDef(value.Enum())
	} else if protoType == pgs.StringT || protoType == pgs.BytesT {
		valueSchema.Type = "string"
	}
	schema.AdditionalProperties = valueSchema
	return schema
}

func buildFromScalaField(field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	schema := &jsonschema.Schema{}
	schema.Title = proto.GetTitleOrEmpty(fo)
	schema.Description = proto.GetDescription(field, fo)

	protoType := field.Type().ProtoType()
	if protoType.IsNumeric() {
		schema.Type = "number"
		fillSchemaByNumericKeywords(schema, fo.GetNumeric())
	} else if protoType == pgs.BoolT {
		schema.Type = "boolean"
	} else if protoType == pgs.EnumT {
		if field.Type().IsRepeated() {
			schema.Ref = toDef(field.Type().Element().Enum())
		} else {
			schema.Ref = toDef(field.Type().Enum())
		}
	} else if protoType == pgs.StringT || protoType == pgs.BytesT {
		schema.Type = "string"
		fillSchemaByStringKeywords(schema, fo.GetString_())
	}

	if field.Type().IsRepeated() {
		return wrapSchemaInArray(schema, field, fo)
	} else {
		return schema
	}
}

func buildFromEnum(enum pgs.Enum) (*jsonschema.Schema, error) {
	eo := proto.GetEnumOptions(enum)

	schema := &jsonschema.Schema{}
	switch eo.GetMappingType() {
	case proto.EnumOptions_MapToString:
		schema.Type = "string"
	case proto.EnumOptions_MapToNumber:
		schema.Type = "number"
	case proto.EnumOptions_MapToCustom:
		schema.Type = "string"
	}
	schema.Title = proto.GetTitleOrEmpty(eo)
	schema.Description = proto.GetDescription(enum, eo)

	for _, enumValue := range enum.Values() {
		switch eo.GetMappingType() {
		case proto.EnumOptions_MapToString:
			schema.Enum = append(schema.Enum, enumValue.Name().String())
		case proto.EnumOptions_MapToNumber:
			schema.Enum = append(schema.Enum, enumValue.Value())
		case proto.EnumOptions_MapToCustom:
			evo := proto.GetEnumValueOptions(enumValue)

			customValue, err := parseScalaValueFromAny(evo.CustomValue)
			if err != nil {
				return nil, err
			}

			if customValue == nil {
				schema.Enum = append(schema.Enum, enumValue.Name().String())
			} else {
				schema.Enum = append(schema.Enum, customValue)
			}
		}

	}
	// TODO: if allow additional values
	return schema, nil
}

func wrapSchemaInArray(schema *jsonschema.Schema, field pgs.Field, fo *proto.FieldOptions) *jsonschema.Schema {
	repeatedSchema := &jsonschema.Schema{}
	repeatedSchema.Title = schema.Title
	repeatedSchema.Description = schema.Description
	repeatedSchema.Type = "array"
	repeatedSchema.Items = schema

	fillSchemaByArrayKeywords(repeatedSchema, fo.GetArray())
	return repeatedSchema
}

func parseScalaValueFromAny(anyValue *anypb.Any) (any, error) {
	if anyValue.Value == nil {
		return nil, nil
	}

	var value any
	if err := json.Unmarshal(anyValue.Value, &value); err != nil {
		return nil, err
	}
	return value, nil
}

func isScalarType(field pgs.Field) bool {
	protoType := field.Type().ProtoType()
	if protoType.IsNumeric() {
		return true
	}
	if protoType == pgs.BoolT {
		return true
	}
	if protoType == pgs.EnumT {
		return true
	}
	if protoType == pgs.StringT || protoType == pgs.BytesT {
		return true
	}
	return false
}

func toPropertyName(name pgs.Name) string {
	return name.String()
}

type FqdnResolver interface {
	FullyQualifiedName() string
}

func toDef(resolver FqdnResolver) string {
	return jsonschema.EmptyID.Def(resolver.FullyQualifiedName()).String()
}
