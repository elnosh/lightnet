package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	dockerpkg "github.com/elnosh/lightnet/cli/docker"
	"github.com/elnosh/lightnet/cli/state"
)

const (
	fanoutValue  = 0.001   // value of each fan-out output
	spendValue   = 0.00099 // 1000 sat fee on the 1-in-1-out spend
	fanoutBatch  = 1000    // outputs per fan-out tx, keeps it under the 100kvB standardness limit
	spendWorkers = 8       // concurrent docker execs while broadcasting
)

// RunFillBlock broadcasts count dummy transactions and mines a block containing
// them. It first fans the test wallet balance out into count confirmed UTXOs,
// then spends each one back to the wallet in its own 1-in-1-out transaction.
func RunFillBlock(networkName string, count int) error {
	net, err := state.GetNetwork(networkName)
	if err != nil {
		return err
	}

	var btcContainer string
	for _, n := range net.Nodes {
		if n.Kind == "bitcoind" {
			btcContainer = n.ContainerName
			break
		}
	}
	if btcContainer == "" {
		return fmt.Errorf("network %q has no bitcoind node", networkName)
	}

	c, err := dockerpkg.NewClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	cli := func(args ...string) (string, error) {
		cmd := append([]string{
			"bitcoin-cli", "-regtest", "-rpcuser=lightnet", "-rpcpassword=lightnet", "-rpcwallet=test",
		}, args...)
		out, err := dockerpkg.ExecOutput(ctx, c, btcContainer, cmd)
		return strings.TrimSpace(out), err
	}

	// Destination for every spend, and the address the blocks are mined to.
	destAddr, err := cli("getnewaddress", "", "bech32")
	if err != nil {
		return fmt.Errorf("getnewaddress: %w", err)
	}

	// createrawtransaction rejects duplicate output addresses, so the fan-out
	// needs one address per output. Generate them all in a single exec — a
	// docker exec per address would dominate the runtime.
	addrsOut, err := dockerpkg.ExecOutput(ctx, c, btcContainer, []string{"sh", "-c", fmt.Sprintf(
		"for i in $(seq %d); do bitcoin-cli -regtest -rpcuser=lightnet -rpcpassword=lightnet"+
			" -rpcwallet=test getnewaddress '' bech32; done", count)})
	if err != nil {
		return fmt.Errorf("generating fan-out addresses: %w", err)
	}
	addrs := strings.Fields(addrsOut)
	if len(addrs) != count {
		return fmt.Errorf("expected %d fan-out addresses, got %d", count, len(addrs))
	}

	fmt.Printf("Fanning out %d outputs of %.8f BTC...\n", count, fanoutValue)
	for remaining := count; remaining > 0; {
		n := min(remaining, fanoutBatch)

		outputs := make([]map[string]float64, n)
		for i := range outputs {
			outputs[i] = map[string]float64{addrs[count-remaining+i]: fanoutValue}
		}
		outputsJSON, err := json.Marshal(outputs)
		if err != nil {
			return err
		}

		raw, err := cli("createrawtransaction", "[]", string(outputsJSON))
		if err != nil {
			return fmt.Errorf("createrawtransaction (fan-out): %w", err)
		}
		fundedOut, err := cli("fundrawtransaction", raw)
		if err != nil {
			return fmt.Errorf("fundrawtransaction (fan-out): %w", err)
		}
		var funded struct {
			Hex string `json:"hex"`
		}
		if err := json.Unmarshal([]byte(fundedOut), &funded); err != nil {
			return fmt.Errorf("parsing fundrawtransaction result: %w", err)
		}
		if _, err := signAndSend(cli, funded.Hex); err != nil {
			return fmt.Errorf("broadcasting fan-out tx: %w", err)
		}

		remaining -= n
	}

	if _, err := cli("generatetoaddress", "1", destAddr); err != nil {
		return fmt.Errorf("confirming fan-out: %w", err)
	}

	// Filtering by the fan-out addresses returns exactly the UTXOs we created,
	// leaving out the change outputs (which went to fresh change addresses).
	addrsJSON, err := json.Marshal(addrs)
	if err != nil {
		return err
	}
	unspentOut, err := cli("listunspent", "1", "9999999", string(addrsJSON))
	if err != nil {
		return fmt.Errorf("listunspent: %w", err)
	}
	var unspent []struct {
		Txid string `json:"txid"`
		Vout int    `json:"vout"`
	}
	if err := json.Unmarshal([]byte(unspentOut), &unspent); err != nil {
		return fmt.Errorf("parsing listunspent result: %w", err)
	}
	if len(unspent) < count {
		return fmt.Errorf("expected %d fan-out UTXOs, found %d", count, len(unspent))
	}
	unspent = unspent[:count]

	fmt.Printf("Broadcasting %d transactions...\n", count)
	jobs := make(chan int)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for range spendWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				inputs, err := json.Marshal([]map[string]any{{"txid": unspent[i].Txid, "vout": unspent[i].Vout}})
				if err != nil {
					errs <- err
					continue
				}
				outputs, err := json.Marshal([]map[string]float64{{destAddr: spendValue}})
				if err != nil {
					errs <- err
					continue
				}
				raw, err := cli("createrawtransaction", string(inputs), string(outputs))
				if err != nil {
					errs <- fmt.Errorf("createrawtransaction: %w", err)
					continue
				}
				if _, err := signAndSend(cli, raw); err != nil {
					errs <- err
				}
			}
		}()
	}
	for i := range unspent {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	close(errs)
	if err := <-errs; err != nil {
		return err
	}

	minedOut, err := cli("generatetoaddress", "1", destAddr)
	if err != nil {
		return fmt.Errorf("mining filled block: %w", err)
	}
	var mined []string
	if err := json.Unmarshal([]byte(minedOut), &mined); err != nil || len(mined) == 0 {
		return fmt.Errorf("parsing generatetoaddress result: %q", minedOut)
	}

	// getblockstats also accepts a height, so bitcoin-cli parses this arg as
	// JSON — the hash has to be quoted to come through as a string.
	statsOut, err := cli("getblockstats", fmt.Sprintf("%q", mined[0]))
	if err != nil {
		return fmt.Errorf("getblockstats: %w", err)
	}
	var stats struct {
		Height      int `json:"height"`
		Txs         int `json:"txs"`
		TotalSize   int `json:"total_size"`
		TotalWeight int `json:"total_weight"`
	}
	if err := json.Unmarshal([]byte(statsOut), &stats); err != nil {
		return fmt.Errorf("parsing getblockstats result: %w", err)
	}

	fmt.Printf("Mined block %d: %s\n", stats.Height, mined[0])
	fmt.Printf("%d txs, %d bytes, %d weight units\n", stats.Txs, stats.TotalSize, stats.TotalWeight)
	return nil
}

// signAndSend signs a raw transaction with the test wallet, broadcasts it and
// returns the txid.
func signAndSend(cli func(args ...string) (string, error), rawHex string) (string, error) {
	signedOut, err := cli("signrawtransactionwithwallet", rawHex)
	if err != nil {
		return "", fmt.Errorf("signrawtransactionwithwallet: %w", err)
	}
	var signed struct {
		Hex      string `json:"hex"`
		Complete bool   `json:"complete"`
	}
	if err := json.Unmarshal([]byte(signedOut), &signed); err != nil {
		return "", fmt.Errorf("parsing signrawtransactionwithwallet result: %w", err)
	}
	if !signed.Complete {
		return "", fmt.Errorf("transaction could not be fully signed")
	}
	txid, err := cli("sendrawtransaction", signed.Hex)
	if err != nil {
		return "", fmt.Errorf("sendrawtransaction: %w", err)
	}
	return txid, nil
}
