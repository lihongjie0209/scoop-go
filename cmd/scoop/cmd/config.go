package cmd

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/scoopinstaller/scoop-go/pkg/app"
	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config [rm] name [value]",
	Short: "Get or set configuration values",
	Long: `Get or set Scoop configuration values.

To get all configuration settings:
  scoop config

To get a configuration setting:
  scoop config <name>

To set a configuration setting:
  scoop config <name> <value>

To remove a configuration setting:
  scoop config rm <name>`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := app.Config()

		if len(args) == 0 {
			// Print all config values using struct field JSON tags.
			c := cfg.Config()
			rv := reflect.ValueOf(*c)
			rt := rv.Type()
			for i := range rt.NumField() {
				field := rt.Field(i)
				tag := field.Tag.Get("json")
				if tag == "" || tag == "-" {
					continue
				}
				key := strings.Split(tag, ",")[0]
				val := rv.Field(i)
				fmt.Printf("%-35s %s\n", key, formatFieldValue(val))
			}
			return nil
		}

		if args[0] == "rm" {
			if len(args) < 2 {
				return fmt.Errorf("usage: scoop config rm <name>")
			}
			if err := cfg.Unset(args[1]); err != nil {
				return err
			}
			cfg.Save()
			fmt.Printf("'%s' has been removed\n", args[1])
			return nil
		}

		name := args[0]
		if len(args) < 2 {
			// Get single value
			val := cfg.Get(name)
			if val == nil {
				fmt.Printf("'%s' is not set\n", name)
			} else {
				fmt.Println(formatFieldValue(reflect.ValueOf(val)))
			}
			return nil
		}

		// Set value
		value := args[1]
		if err := cfg.Set(name, value); err != nil {
			return err
		}
		if err := cfg.Save(); err != nil {
			return err
		}
		fmt.Printf("'%s' has been set to '%s'\n", name, value)
		return nil
	},
}

func boolPtrDisplay(b *bool) string {
	if b == nil {
		return "<not set>"
	}
	return fmt.Sprintf("%v", *b)
}

func displayConfigValue(v interface{}) string {
	if v == nil {
		return "<nil>"
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return "<nil>"
		}
		return displayConfigValue(rv.Elem().Interface())
	}

	return fmt.Sprintf("%v", v)
}

// formatFieldValue returns a human-readable string for a reflect.Value,
// handling pointers, slices, and zero values.
func formatFieldValue(v reflect.Value) string {
	switch v.Kind() {
	case reflect.Ptr:
		if v.IsNil() {
			return "<not set>"
		}
		return formatFieldValue(v.Elem())
	case reflect.Slice:
		if v.IsNil() || v.Len() == 0 {
			return "<not set>"
		}
		return fmt.Sprintf("%v", v.Interface())
	case reflect.Map:
		if v.IsNil() || v.Len() == 0 {
			return "<not set>"
		}
		return fmt.Sprintf("%v", v.Interface())
	case reflect.String:
		if v.String() == "" {
			return "<not set>"
		}
		return v.String()
	case reflect.Bool:
		return fmt.Sprintf("%v", v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v.Int() == 0 {
			return "<not set>"
		}
		return fmt.Sprintf("%d", v.Int())
	case reflect.Interface:
		if v.IsNil() {
			return "<not set>"
		}
		return fmt.Sprintf("%v", v.Elem().Interface())
	default:
		return fmt.Sprintf("%v", v.Interface())
	}
}

func init() {
	rootCmd.AddCommand(configCmd)
}
