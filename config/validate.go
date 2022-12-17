package config

import (
	"fmt"

	log "github.com/sirupsen/logrus"
)

// validate checks for the validity of the JSON configuration loaded in
// Configuration struct.
//
// It does not mutate the configuration values, and returns an error in case of
// invalid configuration.
func (c Configuration) validate() error {
	if err := validateStringField("rpcurl", c.RPCURL); err != nil {
		return err
	}

	if err := validateStringField("rpcuser", c.RPCUser); err != nil {
		return err
	}

	if err := validateStringField("rpcpass", c.RPCPassword); err != nil {
		return err
	}

	for _, account := range c.Accounts {
		if err := validateStringField("external", account.External); err != nil {
			return err
		}
		// if err := validateStringField("internal", account.Internal); err != nil {
		// 	return err
		// }

		if account.Birthday != nil && account.Birthday.Before(BIP0039Genesis) {
			log.WithFields(log.Fields{
				"descriptor": account.External,
				"birthday":   account.Birthday,
			}).Warn("Account birthday older than 2016/06/01")
		}
	}

	return nil
}

func validateStringField(key string, value *string) error {
	if value == nil {
		return fmt.Errorf("%s: %s", ErrMissingKey, key)
	}

	return nil
}
