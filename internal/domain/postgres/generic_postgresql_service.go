package postgres

import (
	"errors"
	"strings"
)

func validateGenericPostgreSQLArgs(args PostgreSQLServiceArgs) error {
	if strings.TrimSpace(args.VMName) == "" || strings.TrimSpace(args.ClusterName) == "" || strings.TrimSpace(args.Namespace) == "" || len(args.Databases) == 0 || strings.TrimSpace(args.ConsumerSecretName) == "" || strings.TrimSpace(args.ConsumerSecretLabel) == "" || strings.TrimSpace(args.ServiceOwner) == "" || strings.TrimSpace(args.ServicePartOf) == "" {
		return errors.New("vmName, clusterName, namespace, databases, consumerSecretName, consumerSecretLabel, serviceOwner, and servicePartOf are required")
	}
	return nil
}
