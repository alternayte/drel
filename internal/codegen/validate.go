package codegen

import "fmt"

// ValidateModels checks the aggregated model set for problems that would
// otherwise produce non-compiling or invalid generated code, and that the
// existing emit pipeline silently tolerated:
//
//   - two models that generate the same DB-struct field name (pluralize(Name)),
//     which yields duplicate struct fields that do not compile;
//   - a relation field whose target model is not in the scanned set (typo or a
//     package missing from drel.yaml), which was silently dropped;
//   - a model with no db-mapped columns, which emits an empty INSERT column list
//     and an invalid `INSERT INTO t () VALUES ()` at runtime.
//
// It is called from Generate before any file is written so a failure leaves the
// working tree untouched.
func ValidateModels(models []ModelInfo) error {
	// Duplicate DB-struct field names (pluralize(Name)) across packages.
	seenField := make(map[string]ModelInfo)
	for _, m := range models {
		field := pluralize(m.Name)
		if prev, ok := seenField[field]; ok {
			return fmt.Errorf("drel: models %s.%s and %s.%s both generate DB field %q; rename one model or split into separate DB structs",
				prev.PkgPath, prev.Name, m.PkgPath, m.Name, field)
		}
		seenField[field] = m
	}

	// Set of model names known to the scan, for relation-target resolution.
	known := make(map[string]bool, len(models))
	for _, m := range models {
		known[m.Name] = true
	}

	for _, m := range models {
		// Relation targets must resolve to a scanned model.
		for _, f := range m.Fields {
			if f.Relation == nil || f.Relation.TargetModel == "" {
				continue
			}
			if relationType(f.Relation.Type) == "" {
				continue // unknown relation kind is ignored elsewhere too
			}
			if !known[f.Relation.TargetModel] {
				return fmt.Errorf("drel: model %q (%s) field %q references unknown model %q; is its package listed under `packages:` in drel.yaml?",
					m.Name, m.PkgPath, f.Name, f.Relation.TargetModel)
			}
		}

		// Every model must declare at least one db-mapped column. A relation-only
		// model emits an empty INSERT column list, which is invalid SQL.
		if len(columnFields(m.Fields)) == 0 {
			return fmt.Errorf("drel: model %q (%s) has no db-mapped columns; a model must declare at least one db-tagged field",
				m.Name, m.PkgPath)
		}
	}

	return validateCompositeKeys(models)
}

// validateCompositeKeys enforces the two limits drel accepts for composite
// primary keys. Both fail at generation time rather than emitting SQL that is
// silently wrong.
//
//  1. A composite-key model cannot be the target of a relationship. A foreign
//     key to it would be a multi-column foreign key, which drel does not yet
//     emit; columnDefSQL always references the target's single key column.
//     The runtime include path depends on the same guarantee: it loads a
//     relationship target by RelatedMeta.PKColumns[0].
//  2. A composite key's column names must be unique within the model, and must
//     not collide with a non-key column.
//
// The second accepted limit, that a composite key cannot be auto-increment,
// needs no check here: the emitter always takes the app-assigned branch for a
// struct key, so the combination is not constructible.
func validateCompositeKeys(models []ModelInfo) error {
	composite := make(map[string]bool, len(models))
	for _, m := range models {
		if m.IsCompositeKey() {
			composite[m.Name] = true
		}
	}

	for _, m := range models {
		for _, f := range m.Fields {
			if f.Relation == nil {
				continue
			}
			if composite[f.Relation.TargetModel] {
				return fmt.Errorf(
					"drel: %s.%s: model %s has a composite primary key and cannot be the target of a relationship; "+
						"multi-column foreign keys are not yet supported. Give %s a single surrogate key, or drop the relationship",
					m.Name, f.Name, f.Relation.TargetModel, f.Relation.TargetModel)
			}
		}

		seen := make(map[string]string, len(m.Key))
		for _, kc := range m.Key {
			if prev, dup := seen[kc.ColumnName]; dup {
				return fmt.Errorf(
					"drel: model %s: key fields %s and %s both map to column %q; give them distinct db tags",
					m.Name, prev, kc.FieldName, kc.ColumnName)
			}
			seen[kc.ColumnName] = kc.FieldName
		}
		for _, f := range columnFields(m.Fields) {
			col := f.ColumnName
			if col == "" {
				col = toSnakeCase(f.Name)
			}
			if keyField, clash := seen[col]; clash {
				return fmt.Errorf(
					"drel: model %s: field %s maps to column %q, which is already a key column (key field %s)",
					m.Name, f.Name, col, keyField)
			}
		}
	}
	return nil
}
