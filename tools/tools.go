package tools

import (
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unsafe"

	"github.com/jtolio/crawlspace/reflectlang"
	"github.com/kr/pretty"
	"github.com/zeebo/goof"
	"github.com/zeebo/sudo"
)

var troop goof.Troop

func assert(err error) {
	if err != nil {
		panic(err)
	}
}

func Env(out io.Writer) reflectlang.Environment {
	env := reflectlang.NewStandardEnvironment()
	env["pretty"] = reflectlang.LowerStruct(env, reflectlang.Environment{
		"Sprint": reflect.ValueOf(pretty.Sprint),
	})

	env["try"] = reflectlang.LowerStruct(env, reflectlang.Environment{
		"E": reflect.ValueOf(assert),
		"E1": reflect.ValueOf(func(a interface{}, err error) (_ interface{}) {
			assert(err)
			return a
		}),
		"E2": reflect.ValueOf(func(a, b interface{}, err error) (_, _ interface{}) {
			assert(err)
			return a, b
		}),
		"E3": reflect.ValueOf(func(a, b, c interface{}, err error) (_, _, _ interface{}) {
			assert(err)
			return a, b, c
		}),
		"E4": reflect.ValueOf(func(a, b, c, d interface{}, err error) (_, _, _, _ interface{}) {
			assert(err)
			return a, b, c, d
		}),
	})

	env["packages"] = reflect.ValueOf(func() []string {
		pkgs := map[string]bool{}
		process := func(names []string) {
			for _, name := range names {
				if strings.HasPrefix(name, "go:") ||
					strings.HasPrefix(name, "struct {") {
					continue
				}
				name = strings.TrimPrefix(name, "type:.eq.")
				name = strings.TrimPrefix(name, "type:.hash.")
				lastSlash := strings.LastIndex(name, "/")
				pkgPrefix := ""
				if lastSlash >= 0 {
					pkgPrefix = name[:lastSlash]
					name = name[lastSlash:]
				}

				pos := strings.Index(name, ".")
				if pos < 0 {
					pkgs[pkgPrefix] = true
					continue
				}
				pkgs[pkgPrefix+name[:pos]] = true
			}
		}

		names, err := troop.Globals()
		assert(err)
		process(names)

		names, err = troop.Functions()
		assert(err)
		process(names)

		types, err := troop.Types()
		assert(err)
		for _, typ := range types {
			pkgs[typ.PkgPath()] = true
		}

		names = make([]string, 0, len(pkgs))
		for pkg := range pkgs {
			names = append(names, pkg)
		}
		sort.Strings(names)
		return names
	})

	filterNames := func(pkg string, names []string) []string {
		pkg += "."
		var filtered []string
		for _, name := range names {
			if strings.HasPrefix(name, pkg) {
				filtered = append(filtered, strings.TrimPrefix(name, pkg))
			}
		}
		sort.Strings(filtered)
		return filtered
	}

	env["globals"] = reflect.ValueOf(func(pkg string) []string {
		rv, err := troop.Globals()
		assert(err)
		return filterNames(pkg, rv)
	})

	env["global"] = reflectlang.LowerFunc(env, func(args []reflect.Value) ([]reflect.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("global expected 2 arguments")
		}
		for _, arg := range args {
			if arg.Kind() != reflect.String {
				return nil, fmt.Errorf("global expected the arguments to be strings")
			}
		}

		pkg := args[0].String()
		name := args[1].String()
		rv, err := troop.Global(pkg + "." + name)
		if err != nil {
			return nil, err
		}
		return []reflect.Value{rv}, nil
	})

	env["functions"] = reflect.ValueOf(func(pkg string) []string {
		rv, err := troop.Functions()
		assert(err)
		return filterNames(pkg, rv)
	})

	env["types"] = reflect.ValueOf(func(pkg string) []string {
		rv, err := troop.Types()
		assert(err)
		var names []string
		for _, typ := range rv {
			if typ.PkgPath() == pkg {
				names = append(names, typ.Name())
			}
		}
		sort.Strings(names)
		return names
	})

	env["filter"] = reflect.ValueOf(func(haystack []string, needle string) []string {
		var rv []string
		for _, hay := range haystack {
			if strings.Contains(hay, needle) {
				rv = append(rv, hay)
			}
		}
		return rv
	})

	env["call"] = reflectlang.LowerFunc(env, func(args []reflect.Value) (_ []reflect.Value, err error) {
		if len(args) < 2 {
			return nil, fmt.Errorf("call expected at least 2 arguments")
		}
		if args[0].Kind() != reflect.String {
			return nil, fmt.Errorf("call expected the first argument to be a string")
		}
		if args[1].Kind() != reflect.String {
			return nil, fmt.Errorf("call expected the second argument to be a string")
		}

		iargs := make([]interface{}, 0, len(args)-2)
		for _, arg := range args[2:] {
			// TODO: can we leave these reflect.Values?
			iargs = append(iargs, arg.Interface())
		}

		results, err := troop.Call(args[0].String()+"."+args[1].String(), iargs...)
		if err != nil {
			return nil, err
		}

		var iresults []reflect.Value
		for _, res := range results {
			iresults = append(iresults, reflect.ValueOf(res))
		}

		return iresults, nil
	})

	env["newAt"] = reflectlang.LowerFunc(env, func(args []reflect.Value) ([]reflect.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("newAt expected 3 arguments")
		}
		if args[0].Kind() != reflect.String {
			return nil, fmt.Errorf("newAt expected the first argument to be a string")
		}
		if args[1].Kind() != reflect.String {
			return nil, fmt.Errorf("newAt expected the second argument to be a string")
		}
		if !args[2].CanInt() {
			return nil, fmt.Errorf("newAt expected the third argument to be an integer")
		}

		typ, err := troop.Type(args[0].String() + "." + args[1].String())
		if err != nil {
			return nil, err
		}
		return []reflect.Value{reflect.NewAt(typ, unsafe.Pointer(uintptr(args[2].Int())))}, nil
	})

	env["dir"] = reflect.ValueOf(func(args ...interface{}) []string {
		handleEnv := func(env reflectlang.Environment) []string {
			names := []string{}
			for key := range env {
				if !strings.HasPrefix(key, "$") {
					names = append(names, key)
				}
			}
			sort.Strings(names)
			return names
		}
		if len(args) == 0 {
			return handleEnv(env)
		}

		if sub := reflectlang.IsLowerStruct(args[0]); sub != nil {
			return handleEnv(sub)
		}
		if reflectlang.IsLowerFunc(args[0]) {
			return []string{}
		}

		fields := []string{}
		handle := func(typ reflect.Type) {
			for i := 0; i < typ.NumMethod(); i++ {
				fields = append(fields, typ.Method(i).Name)
			}
			if typ.Kind() == reflect.Struct {
				for i := 0; i < typ.NumField(); i++ {
					fields = append(fields, typ.Field(i).Name)
				}
			}
		}

		arg := args[0]
		typ := reflect.TypeOf(arg)
		handle(typ)
		if typ.Kind() == reflect.Pointer {
			handle(typ.Elem())
		}
		sort.Strings(fields)
		return fields
	})

	env["println"] = reflect.ValueOf(func(args ...interface{}) {
		_, err := fmt.Fprintln(out, args...)
		assert(err)
	})

	env["printf"] = reflect.ValueOf(func(msgf string, args ...interface{}) {
		_, err := fmt.Fprintf(out, msgf, args...)
		assert(err)
	})

	env["sudo"] = reflectlang.LowerFunc(env, func(args []reflect.Value) ([]reflect.Value, error) {
		result := make([]reflect.Value, 0, len(args))
		for _, arg := range args {
			result = append(result, sudo.Sudo(arg))
		}
		return result, nil
	})

	env["$import"] = reflectlang.LowerFunc(env, func(args []reflect.Value) ([]reflect.Value, error) {

		if len(args) != 2 {
			return nil, fmt.Errorf("import expected 2 arguments")
		}
		if args[0].Kind() != reflect.String {
			return nil, fmt.Errorf("import expected a target name argument")
		}
		if args[1].Kind() != reflect.String {
			return nil, fmt.Errorf("import expected a package name")
		}

		target := args[0].String()
		pkgName := args[1].String()

		if target == "_" {
			return nil, nil
		}
		var envToFill reflectlang.Environment
		if target == "." {
			envToFill = env
		} else {
			if target == "" {
				target = importPathToNameBasic(pkgName)
			}
			envToFill = reflectlang.Environment{}

			env[target] = reflectlang.LowerStruct(env, envToFill)
		}

		scanList := func(names []string, loader func(name string) (reflect.Value, error)) error {
			for _, name := range names {
				if !strings.HasPrefix(name, pkgName+".") {
					continue
				}
				localName := strings.TrimPrefix(name, pkgName+".")
				if !reflectlang.IsIdentifier(localName) {
					continue
				}
				global, err := loader(name)
				if err != nil {
					return err
				}
				envToFill[localName] = global
			}
			return nil
		}

		globals, err := troop.Globals()
		if err != nil {
			return nil, err
		}
		if err = scanList(globals, troop.Global); err != nil {
			return nil, err
		}

		functions, err := troop.Functions()
		if err != nil {
			return nil, err
		}
		return nil, scanList(functions, func(name string) (reflect.Value, error) {
			return reflectlang.LowerFunc(env, func(args []reflect.Value) (_ []reflect.Value, err error) {
				iargs := make([]interface{}, 0, len(args))
				for _, arg := range args {
					// TODO: can we leave these reflect.Values?
					iargs = append(iargs, arg.Interface())
				}

				results, err := troop.Call(name, iargs...)
				if err != nil {
					return nil, err
				}

				var iresults []reflect.Value
				for _, res := range results {
					iresults = append(iresults, reflect.ValueOf(res))
				}

				return iresults, nil
			}), nil
		})
	})

	return env
}
