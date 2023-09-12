package integration

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ipfs/go-cid"
	unixfs "github.com/ipfs/go-unixfsnode/testutil"
	"github.com/ipld/go-car/v2"
	"github.com/ipld/go-car/v2/storage"
	cidlink "github.com/ipld/go-ipld-prime/linking/cid"
	"github.com/ipni/storetheindex/test"
	"github.com/stretchr/testify/require"
)

const rseed = 1234

func TestIpni(t *testing.T) {
	req := require.New(t)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()

	indexerReady := test.NewIndexerReadyWatcher()
	frisbiiReady := test.NewStdoutWatcher("frisbii", "Announce() complete")

	tr := test.NewTestIndexerRunner(t, ctx, t.TempDir(), indexerReady, frisbiiReady)

	t.Log("Running in test directory:", tr.Dir)

	// install the frisbii cmd, when done in tr.Run() will use the GOPATH/GOBIN
	// in the test directory, so we get a localised `frisbii` executable
	frisbii := filepath.Join(tr.Dir, "frisbii")
	tr.Run("go", "install", "../../cmd/frisbii/")

	cwd, err := os.Getwd()
	req.NoError(err)
	err = os.Chdir(tr.Dir)
	req.NoError(err)

	// install the indexer to announce to
	indexer := filepath.Join(tr.Dir, "storetheindex")
	tr.Run("go", "install", "github.com/ipni/storetheindex@e56485343dd8e235581191b4668b5bfc0cea0781") // TODO: use @latest when we have a release
	// install the ipni cli to inspect the indexer
	ipni := filepath.Join(tr.Dir, "ipni")
	tr.Run("go", "install", "github.com/ipni/ipni-cli/cmd/ipni@latest")

	err = os.Chdir(cwd)
	req.NoError(err)

	// initialise and start the indexer and adjust the config
	tr.Run(indexer, "init", "--store", "pebble", "--pubsub-topic", "/indexer/ingest/mainnet", "--no-bootstrap")
	cmdIndexer := tr.Start(indexer, "daemon")
	select {
	case <-indexerReady.Signal:
	case <-ctx.Done():
		t.Fatal("timed out waiting for indexer to start")
	}

	/*
		We don't seem to need to give it explicit permission, but if we do, here it is

		// loading a private key will generate an ID before we start frisbii
		confDir := filepath.Join(tr.Dir, util.FrisbiiConfigDir)
		if _, err := os.Stat(confDir); os.IsNotExist(err) {
			req.NoError(os.Mkdir(confDir, 0700))
		}
		_, id, err := util.LoadPrivKey(confDir)
		req.NoError(err)

		// Allow provider advertisements, regardless of default policy.
		tr.Run(indexer, "admin", "allow", "-i", "http://localhost:3002", "--peer", id.String())
	*/

	// setup the frisbii CLI args
	args := []string{
		"--listen", "localhost:37471",
		"--announce", "roots",
		"--announce-url", "http://localhost:3001/announce",
		"--verbose",
	}

	// make some CARs to announce and put them in the args
	cars := mkCars(t, 4)
	for _, carPath := range cars {
		args = append(args, "--car", carPath)
	}

	// start frisbii
	cmdFrisbii := tr.Start(frisbii, args...)

	select {
	case <-frisbiiReady.Signal:
	case <-ctx.Done():
		t.Fatal("timed out waiting for frisbii to announce")
	}

	// wait for the CARs to be indexed
	req.Eventually(func() bool {
		for mh := range cars {
			findOutput := tr.Run(ipni, "find", "--no-priv", "-i", "http://localhost:3000", "-mh", mh)
			t.Logf("import output:\n%s\n", findOutput)

			if bytes.Contains(findOutput, []byte("not found")) {
				return false
			}
			if !bytes.Contains(findOutput, []byte("Provider:")) {
				t.Logf("mh %s: unexpected error: %s", mh, findOutput)
				return false
			}
			t.Logf("mh %s: found", mh)
		}
		return true
	}, 10*time.Second, time.Second)

	// stop and clean up
	tr.Stop(cmdIndexer, time.Second)
	tr.Stop(cmdFrisbii, time.Second)
}

func mkCars(t *testing.T, count int) map[string]string {
	req := require.New(t)

	carDir := t.TempDir()
	cars := make(map[string]string, count)
	rndReader := rand.New(rand.NewSource(int64(rseed)))

	for i := 0; i < count; i++ {
		carPath := filepath.Join(carDir, fmt.Sprintf("test-%d.car", i))
		carFile, err := os.Create(carPath)
		req.NoError(err)
		carWriter, err := storage.NewWritable(carFile, []cid.Cid{cid.MustParse("baeaaaiaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, car.WriteAsCarV1(true))
		req.NoError(err)
		lsys := cidlink.DefaultLinkSystem()
		lsys.SetWriteStorage(carWriter)
		dirEnt := unixfs.GenerateFile(t, &lsys, rndReader, 1<<20)
		err = carFile.Close()
		req.NoError(err)
		err = car.ReplaceRootsInFile(carFile.Name(), []cid.Cid{dirEnt.Root})
		req.NoError(err)
		cars[dirEnt.Root.Hash().B58String()] = carFile.Name()
	}

	return cars
}
