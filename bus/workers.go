package bus

import (
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/btcsuite/btcd/rpcclient"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/ledgerhq/satstack/config"
	"github.com/ledgerhq/satstack/utils"
	log "github.com/sirupsen/logrus"
)

func waitForIBD(b *Bus) error {
	for {
		info, err := b.mainClient.GetBlockChainInfo()
		if err != nil {
			return err
		}

		if info.Blocks != info.Headers {
			log.WithFields(log.Fields{
				"prefix":   "worker",
				"count":    fmt.Sprintf("%d/%d", info.Blocks, info.Headers),
				"progress": fmt.Sprintf("%.2f%%", info.VerificationProgress*100),
			}).Info("Performing Initial Block Download")
		} else {
			log.WithFields(log.Fields{
				"prefix":      "worker",
				"blockHeight": info.Blocks,
				"blockHash":   info.BestBlockHash,
			}).Info("Initial Block Download complete")

			break
		}

		time.Sleep(7 * time.Second)
	}

	return nil
}

func getImportProgress(b *Bus) (bool, error) {
	walletInfo, err := b.secondaryClient.GetWalletInfo()
	if err != nil {
		return false, err
	}

	switch v := walletInfo.Scanning.Value.(type) {
	case btcjson.ScanProgress:
		log.WithFields(log.Fields{
			"prefix":   "worker",
			"progress": fmt.Sprintf("%.2f%%", v.Progress*100),
			"duration": utils.HumanizeDuration(
				time.Duration(v.Duration) * time.Second),
		}).Info("Importing descriptors")
	default:
		// Not scanning currently, or scan is complete.
	}

	return true, nil
}

// ImportAccounts will import the descriptors corresponding to the accounts
// into the Bitcoin Core wallet. This is a blocking operation.
func (b *Bus) ImportAccounts(accounts []config.Account) error {
	// Skip import of descriptors, if no account config found. SatStack
	// will run in zero-configuration mode.
	if accounts == nil {
		return nil
	}

	// ToDo Include My Code here

	client, err := b.ClientFactory()
	if err != nil {
		return err
	}

	defer client.Shutdown()

	var allDescriptors []descriptor
	for _, account := range accounts {
		accountDescriptors, err := descriptors(client, account)
		if err != nil {
			return err // return bare error, since it already has a ctx
		}

		allDescriptors = append(allDescriptors, accountDescriptors...)
	}

	var descriptorsToImport []descriptor
	for _, descriptor := range allDescriptors {
		address, err := DeriveAddress(client, descriptor.Value, descriptor.Depth)
		if err != nil {
			return fmt.Errorf("%s (%s - #%d): %w",
				ErrDeriveAddress, descriptor.Value, descriptor.Depth, err)
		}

		addressInfo, err := client.GetAddressInfo(*address)
		if err != nil {
			return fmt.Errorf("%s (%s): %w", ErrAddressInfo, *address, err)
		}

		if !addressInfo.IsWatchOnly {
			descriptorsToImport = append(descriptorsToImport, descriptor)
		}
	}

	for _, desc := range descriptorsToImport {
		fmt.Println(desc)
	}

	if len(descriptorsToImport) == 0 {
		log.WithField(
			"prefix", "worker",
		).Info("No (new) descriptors to import")
		return nil
	}

	return ImportDescriptors(client, descriptorsToImport)
}

// descriptors returns canonical descriptors from the account configuration.
func descriptors(client *rpcclient.Client, account config.Account) ([]descriptor, error) {
	var ret []descriptor

	var depth int
	switch account.Depth {
	case nil:
		depth = defaultAccountDepth
	default:
		depth = *account.Depth
	}

	var age uint32
	switch account.Birthday {
	case nil:
		age = uint32(config.BIP0039Genesis.Unix())
	default:
		age = uint32(account.Birthday.Unix())
	}

	rawDescs := []string{
		strings.Split(*account.External, "#")[0], // strip out the checksum
		strings.Split(*account.Internal, "#")[0], // strip out the checksum
	}

	for _, desc := range rawDescs {
		canonicalDesc, err := GetCanonicalDescriptor(client, desc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ErrInvalidDescriptor, err)
		}

		ret = append(ret, descriptor{
			Value: *canonicalDesc,
			Depth: depth,
			Age:   age,
		})
	}

	return ret, nil
}

// // ImportAccounts will import the descriptors corresponding to the accounts
// // into the Bitcoin Core wallet. This is a blocking operation.
// func (b *Bus) AbortRescan(ctx context.Context) (err error) {

// 	walletInfo, err := b.secondaryClient.GetWalletInfo()
// 	if err != nil {
// 		return err
// 	}

// 	switch v := walletInfo.Scanning.Value.(type) {
// 	case btcjson.ScanProgress:
// 		log.WithFields(log.Fields{
// 			"prefix":   "AbortRescan",
// 			"progress": fmt.Sprintf("%.2f%%", v.Progress*100),
// 			"duration": utils.HumanizeDuration(
// 				time.Duration(v.Duration) * time.Second),
// 		}).Info("Aborting Rescan")

// 	default:
// 		// Not scanning currently, or scan is complete.
// 		log.WithFields(log.Fields{
// 			"prefix": "AbortRescan",
// 		}).Info("wallet is not scanning")
// 		return nil
// 	}
// 	var params []json.RawMessage
// 	var abortRescan bool
// 	result, err := b.secondaryClient.RawRequest("abortrescan", params)

// 	if err != nil {
// 		log.WithFields(log.Fields{
// 			"prefix": "AbortRescan",
// 			"error":  err,
// 		}).Error("Failed to abort wallet rescan")

// 		return err
// 	}

// 	err = json.Unmarshal(result, &abortRescan)

// 	if err != nil {
// 		log.WithFields(log.Fields{
// 			"prefix": "AbortRescan",
// 			"error":  err,
// 		}).Error("Unmarshal abortRescan result")

// 		return err
// 	}

// 	log.WithFields(log.Fields{
// 		"prefix": "AbortRescan",
// 	}).Infof("Abort Rescan successful: %t", abortRescan)

// 	return nil

// }

// runTheNumbers performs inflation checks against the connected full node.
//
// It does NOT perform any equality comparison between expected and actual
// supply.
func runTheNumbers(b *Bus) error {
	log.WithField("prefix", "worker").Info("Computing circulating supply...")

	info, err := b.mainClient.GetTxOutSetInfo()
	if err != nil {
		return err
	}

	const halvingBlocks = 210000

	var (
		subsidy float64 = 50
		supply  float64 = 0
	)

	i := int64(0)
	for ; i < info.Height/halvingBlocks; i++ {
		supply += halvingBlocks * subsidy
		subsidy /= 2
	}

	supply += subsidy * float64(info.Height-(halvingBlocks*i))

	supplyBTC, err := btcutil.NewAmount(supply)
	if err != nil {
		return err
	}

	log.WithFields(log.Fields{
		"prefix":         "worker",
		"height":         info.Height,
		"expectedSupply": supplyBTC,
		"actualSupply":   info.TotalAmount,
	}).Info("#RunTheNumbers successful")

	return nil
}

func (b *Bus) Worker(config *config.Configuration, skipCirculationCheck bool) {
	importDone := make(chan bool)

	sendInterruptSignal := func() {
		pid := syscall.Getpid()
		p, err := os.FindProcess(pid)
		if err != nil {
			log.WithFields(log.Fields{
				"prefix": "worker",
				"pid":    pid,
				"error":  err,
			}).Fatal("Failed to find process")
			return
		}

		if err := p.Signal(os.Interrupt); err != nil {
			log.WithFields(log.Fields{
				"prefix": "worker",
				"pid":    pid,
				"error":  err,
			}).Fatal("Failed to send INTERRUPT signal")
		}
	}

	go func() {
		if err := waitForIBD(b); err != nil {
			log.WithFields(log.Fields{
				"prefix": "worker",
				"error":  err,
			}).Error("Failed during Initial Block Download")

			sendInterruptSignal()
			return
		}

		// b.IsPendingScan = true

		// if err := runTheNumbers(b); err != nil {
		// 	log.WithFields(log.Fields{
		// 		"prefix": "worker-runthenumbers",
		// 		"error":  err,
		// 	}).Error("Failed while running the numbers")

		// 	sendInterruptSignal()
		// 	return
		// }

		b.IsPendingScan = false

		if err := b.ImportAccounts(config.Accounts); err != nil {
			log.WithFields(log.Fields{
				"prefix": "worker-importacounts",
				"error":  err,
			}).Error("Failed while importing descriptors")

			sendInterruptSignal()
			return
		} else {
			log.WithFields(log.Fields{
				"prefix": "worker-importacounts",
			}).Info("Successfully imported descriptors")
		}

		importDone <- true
	}()

	go func() {
		defer func() {
			close(importDone)

			log.WithFields(log.Fields{
				"prefix": "worker",
			}).Info("Shutdown worker: done")
		}()

		for {
			select {
			case <-importDone:
				return

			default:
				time.Sleep(7 * time.Second)

				if _, err := getImportProgress(b); err != nil {
					log.WithFields(log.Fields{
						"prefix": "worker",
						"error":  err,
					}).Error("Failed to query wallet state")

					sendInterruptSignal()
					return
				}
			}
		}
	}()
}
