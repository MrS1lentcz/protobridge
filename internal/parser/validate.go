package parser

import (
	"fmt"

	"google.golang.org/protobuf/types/descriptorpb"
)

func validate(api *ParsedAPI, msgMap map[string]*descriptorpb.DescriptorProto) error {
	if err := validateOneofUniqueness(api, msgMap); err != nil {
		return err
	}
	if err := validateOneofInputConstraint(api); err != nil {
		return err
	}
	if err := validateOneofDiscriminatorField(api, msgMap); err != nil {
		return err
	}
	if err := validateStreamingOptions(api); err != nil {
		return err
	}
	if err := validateQueryParamsTarget(api, msgMap); err != nil {
		return err
	}
	if err := validateAuthMethod(api, msgMap); err != nil {
		return err
	}
	return nil
}

// validateOneofUniqueness ensures that message type names used inside oneof
// blocks are globally unique across the entire API. The same message type
// used by multiple methods is only checked once.
func validateOneofUniqueness(api *ParsedAPI, msgMap map[string]*descriptorpb.DescriptorProto) error {
	seen := make(map[string]string)      // oneof variant message name → location
	checked := make(map[string]bool)     // message FullName → already validated

	checkMessage := func(mt *MessageType, location string) error {
		if checked[mt.FullName] {
			return nil
		}
		checked[mt.FullName] = true

		for _, od := range mt.OneofDecls {
			for _, v := range od.Variants {
				if !v.IsMessage {
					continue
				}
				key := v.MessageName
				loc := fmt.Sprintf("%s (oneof %s)", location, od.Name)
				if prev, exists := seen[key]; exists {
					return fmt.Errorf("oneof message type %q used in %s conflicts with %s – names must be globally unique", key, loc, prev)
				}
				seen[key] = loc
			}
		}
		return nil
	}

	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			loc := svc.Name + "." + m.Name
			if m.InputType != nil {
				if err := checkMessage(m.InputType, loc+" input"); err != nil {
					return err
				}
			}
			if m.OutputType != nil {
				if err := checkMessage(m.OutputType, loc+" output"); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// validateQueryParamsTarget ensures the target field exists as a message-typed
// field in the request message.
func validateQueryParamsTarget(api *ParsedAPI, msgMap map[string]*descriptorpb.DescriptorProto) error {
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.QueryParamsTarget == "" || m.InputType == nil {
				continue
			}
			found := false
			for _, f := range m.InputType.Fields {
				if f.Name == m.QueryParamsTarget && f.Type == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("query_params_target %q in %s.%s does not reference a message-typed field in %s",
					m.QueryParamsTarget, svc.Name, m.Name, m.InputType.Name)
			}
		}
	}
	return nil
}

// validateAuthMethod checks that the auth method's input has a map<string,string>
// field (headers) and that it does not have an HTTP annotation.
func validateAuthMethod(api *ParsedAPI, msgMap map[string]*descriptorpb.DescriptorProto) error {
	if api.AuthMethod == nil {
		return nil
	}

	am := api.AuthMethod
	if am.InputType == nil {
		return fmt.Errorf("auth_method %s.%s has an unresolvable input type", am.ServiceName, am.MethodName)
	}

	// Verify the input message has a map<string,string> field.
	hasMapField := false
	for _, f := range am.InputType.Fields {
		if f.Type == descriptorpb.FieldDescriptorProto_TYPE_MESSAGE && f.TypeName != "" {
			// Check if this is a map entry.
			if entry, ok := msgMap[f.TypeName]; ok && entry.GetOptions().GetMapEntry() {
				hasMapField = true
				break
			}
		}
	}
	if !hasMapField {
		return fmt.Errorf("auth_method %s.%s input message %s must have a map<string,string> field for headers",
			am.ServiceName, am.MethodName, am.InputType.Name)
	}

	return nil
}

// validateOneofInputConstraint ensures that message types used as oneof
// variants are never used as standalone RPC input types. This prevents
// inconsistency where the same message would sometimes carry a discriminator
// field and sometimes not.
func validateOneofInputConstraint(api *ParsedAPI) error {
	// Collect all message names that appear as oneof variants.
	oneofMessages := make(map[string]string) // message FullName → location

	collectOneofVariants := func(mt *MessageType, location string) {
		if mt == nil {
			return
		}
		for _, od := range mt.OneofDecls {
			for _, v := range od.Variants {
				if v.IsMessage {
					// We only have unqualified names from variants, but we need
					// to match against FullName of input types. Collect unqualified
					// names and match below.
					oneofMessages[v.MessageName] = fmt.Sprintf("%s (oneof %s)", location, od.Name)
				}
			}
		}
	}

	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			loc := svc.Name + "." + m.Name
			collectOneofVariants(m.InputType, loc)
			collectOneofVariants(m.OutputType, loc)
		}
	}

	// Check that no RPC uses a oneof variant message as its input type.
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			if m.InputType == nil {
				continue
			}
			if loc, exists := oneofMessages[m.InputType.Name]; exists {
				return fmt.Errorf(
					"message %q is used as a oneof variant in %s and cannot be used as standalone RPC input in %s.%s – "+
						"oneof variant messages carry a discriminator field and must not appear outside of oneof blocks",
					m.InputType.Name, loc, svc.Name, m.Name)
			}
		}
	}

	return nil
}

// validateOneofDiscriminatorField ensures that messages used as oneof variants
// do not define a field named "protobridge_disc", since that name is reserved
// for the generated discriminator.
func validateOneofDiscriminatorField(api *ParsedAPI, msgMap map[string]*descriptorpb.DescriptorProto) error {
	const reserved = "protobridge_disc"
	checked := make(map[string]bool)

	checkMessage := func(mt *MessageType, location string) error {
		if mt == nil {
			return nil
		}
		for _, od := range mt.OneofDecls {
			for _, v := range od.Variants {
				if !v.IsMessage || checked[v.MessageName] {
					continue
				}
				checked[v.MessageName] = true

				// Find this message in the msgMap and check its fields.
				for fqn, desc := range msgMap {
					if desc.GetName() != v.MessageName {
						continue
					}
					for _, f := range desc.Field {
						if f.GetName() == reserved {
							return fmt.Errorf(
								"message %q (used as oneof variant in %s, oneof %s) defines a field named %q – "+
									"this field name is reserved by protobridge for the union discriminator",
								v.MessageName, location, od.Name, reserved)
						}
					}
					_ = fqn
					break
				}
			}
		}
		return nil
	}

	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			loc := svc.Name + "." + m.Name
			if err := checkMessage(m.InputType, loc); err != nil {
				return err
			}
			if err := checkMessage(m.OutputType, loc); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateStreamingOptions checks SSE and ws_mode constraints.
func validateStreamingOptions(api *ParsedAPI) error {
	for _, svc := range api.Services {
		for _, m := range svc.Methods {
			loc := svc.Name + "." + m.Name

			// SSE is only valid on server-streaming RPCs.
			if m.SSE {
				if m.StreamType != StreamServer {
					return fmt.Errorf("sse option on %s is only valid on server-streaming RPCs", loc)
				}
			}

			// ws_mode is only valid on streaming RPCs.
			if m.WSMode != "" {
				if m.StreamType == StreamUnary {
					return fmt.Errorf("ws_mode option on %s is only valid on streaming RPCs", loc)
				}
				if m.WSMode != "private" && m.WSMode != "broadcast" {
					return fmt.Errorf("ws_mode on %s must be \"private\" or \"broadcast\", got %q", loc, m.WSMode)
				}
				// SSE + ws_mode is redundant but not invalid (SSE can be private or broadcast too).
			}
		}
	}
	return nil
}
