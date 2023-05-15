package checksumfile

import (
	"bytes"
	"crypto/md5"
	crypto_rand "crypto/rand"
	"encoding/binary"
	"fmt"
	"hash"
	"io/ioutil"
	"math/rand"
	"os"
	"path/filepath"
	"syscall"

	"github.com/rubrikinc/kronos/protoutil"

	"github.com/pkg/errors"
)

var randLetters = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

// RandBytes returns a byte slice of the given length with random
// data.
func RandBytes(r *rand.Rand, size int) []byte {
	if size <= 0 {
		return nil
	}

	arr := make([]byte, size)
	for i := 0; i < len(arr); i++ {
		arr[i] = randLetters[r.Intn(len(randLetters))]
	}
	return arr
}

func NewPseudoRand() (*rand.Rand, int64) {
	var seed int64
	if err := binary.Read(crypto_rand.Reader, binary.LittleEndian, &seed); err != nil {
		panic(fmt.Sprintf("could not read from crypto/rand: %s", err))
	}
	return rand.New(rand.NewSource(seed)), seed
}

// ErrChecksumMismatch is returned when checksum and data don't match for a file
var ErrChecksumMismatch = errors.New("checksum and data don't match")

func tempFileSuffix() string {
	rng, _ := NewPseudoRand()
	return "tmp." + string(RandBytes(rng, 6))
}

// checksumedFile only supports complete rewrites. It internally serializes the
// content and checksum in a binary format before writing.
type checksumedFile struct {
	filename      string
	hashGenerator hash.Hash
}

func newChecksumedFile(name string) checksumedFile {
	return checksumedFile{filename: name, hashGenerator: md5.New()}
}

func (c *checksumedFile) read() ([]byte, error) {
	content, err := ioutil.ReadFile(c.filename)
	if err != nil {
		return nil, err
	}
	fe := &FileExtent{}
	if err := protoutil.Unmarshal(content, fe); err != nil {
		return nil, err
	}
	if !valid(fe.Checksum, fe.Data, c.hashGenerator) {
		return nil, ErrChecksumMismatch
	}
	return fe.Data, nil
}

func (c *checksumedFile) write(p []byte) error {
	cksm, err := computeHash(p, c.hashGenerator)
	if err != nil {
		return err
	}
	fe, err := protoutil.Marshal(&FileExtent{Checksum: cksm, Data: p})
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(c.filename, fe, 0644); err != nil {
		return err
	}
	return sync(c.filename)
}

// Read reads data written to filename using the Write function. It returns an
// error if the checksums don't match or file doesn't exist.
func Read(filename string) ([]byte, error) {
	cksmFile := newChecksumedFile(filename)
	contents, err := cksmFile.read()
	if err != nil {
		return nil, errors.Wrapf(err, "could not read from file %s", filename)
	}
	return contents, nil
}

func sync(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return err
	}
	return f.Close()
}

// Write writes p to filename along with its checksum in a binary format.
// This data can be read using the Read function. Write returns an error if
// data could not be completely written for some reason. It never corrupts the
// existing file.
func Write(filename string, p []byte) error {
	tempFileName := filename + tempFileSuffix()
	tempChecksumedFile := newChecksumedFile(tempFileName)
	if err := tempChecksumedFile.write(p); err != nil {
		return errors.Wrapf(err, "could not write to temp file %s", tempFileName)
	}
	if wb, err := tempChecksumedFile.read(); err != nil || !bytes.Equal(wb, p) {
		return errors.Wrapf(err, "could not validate data written to temp file %s", tempFileName)
	}
	if err := syscall.Rename(tempFileName, filename); err != nil {
		return errors.Wrapf(err, "could not rename temp file %s to %s", tempFileName, filename)
	}
	// Sync the directory to make the rename durable.
	return sync(filepath.Dir(filename))
}
