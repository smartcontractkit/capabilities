package standalone

import (
	"fmt"
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

func BindWithEnvVar(flag *pflag.Flag) {
	formatted := strings.ToUpper(strings.ReplaceAll(flag.Name, "-", "_"))
	creEnv := "CRE_" + formatted
	clEnv := "CL_" + formatted
	flag.Usage += fmt.Sprintf("[env in order: %s, %s]", creEnv, clEnv)
	_ = viper.BindPFlag(flag.Name, flag)
	_ = viper.BindEnv(flag.Name, creEnv, clEnv)
}
