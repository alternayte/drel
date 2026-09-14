package codegen

import "sort"

// enumTypeKey identifies an enum type across models. Two packages can declare
// an enum with the same local name, so the key carries the package path.
func enumTypeKey(f FieldInfo) string {
	return f.TypePkgPath + "." + f.LocalGoType
}

// assignEnumOwners gives each enum type of a package to exactly one model of
// that package. The generated file of that model declares the Values() and
// IsValid() helpers; the files of the other models that use the same enum
// declare nothing. Without this, two models of one package that share an enum
// type produce duplicate declarations and the package does not compile.
//
// The owner is the model with the lowest name, so the assignment does not
// depend on the scan order.
func assignEnumOwners(models []ModelInfo) {
	byPkg := make(map[string][]int)
	for i := range models {
		byPkg[models[i].PkgPath] = append(byPkg[models[i].PkgPath], i)
	}
	for _, idxs := range byPkg {
		sort.Slice(idxs, func(a, b int) bool {
			return models[idxs[a]].Name < models[idxs[b]].Name
		})
		owned := make(map[string]bool)
		for _, i := range idxs {
			models[i].EnumOwners = make(map[string]bool)
			for _, f := range columnFields(models[i].Fields) {
				if !f.IsEnum || len(f.EnumValues) == 0 {
					continue
				}
				key := enumTypeKey(f)
				if owned[key] {
					continue
				}
				owned[key] = true
				models[i].EnumOwners[key] = true
			}
		}
	}
}
