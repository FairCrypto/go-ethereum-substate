package substate

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethdb"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	stage1SubstatePrefix = "1s" // stage1SubstatePrefix + block (64-bit) + tx (64-bit) -> substateRLP
	stage1CodePrefix     = "1c" // stage1CodePrefix + codeHash (256-bit) -> code
)

func Stage1SubstateKey(block uint64, tx int) []byte {
	prefix := []byte(stage1SubstatePrefix)

	blockTx := make([]byte, 16)
	binary.BigEndian.PutUint64(blockTx[0:8], block)
	binary.BigEndian.PutUint64(blockTx[8:16], uint64(tx))

	return append(prefix, blockTx...)
}

func DecodeStage1SubstateKey(key []byte) (block uint64, tx int, err error) {
	prefix := stage1SubstatePrefix
	if len(key) != len(prefix)+8+8 {
		err = fmt.Errorf("invalid length of stage1 substate key: %v", len(key))
		return
	}
	if p := string(key[:len(prefix)]); p != prefix {
		err = fmt.Errorf("invalid prefix of stage1 substate key: %#x", p)
		return
	}
	blockTx := key[len(prefix):]
	block = binary.BigEndian.Uint64(blockTx[0:8])
	tx = int(binary.BigEndian.Uint64(blockTx[8:16]))
	return
}

func Stage1SubstateBlockPrefix(block uint64) []byte {
	prefix := []byte(stage1SubstatePrefix)

	blockBytes := make([]byte, 8)
	binary.BigEndian.PutUint64(blockBytes[0:8], block)

	return append(prefix, blockBytes...)
}

func Stage1CodeKey(codeHash common.Hash) []byte {
	prefix := []byte(stage1CodePrefix)
	return append(prefix, codeHash.Bytes()...)
}

func DecodeStage1CodeKey(key []byte) (codeHash common.Hash, err error) {
	prefix := stage1CodePrefix
	if len(key) != len(prefix)+32 {
		err = fmt.Errorf("invalid length of stage1 code key: %v", len(key))
		return
	}
	if p := string(key[:2]); p != prefix {
		err = fmt.Errorf("invalid prefix of stage1 code key: %#x", p)
		return
	}
	codeHash = common.BytesToHash(key[len(prefix):])
	return
}

type BackendDatabase interface {
	ethdb.KeyValueReader
	ethdb.KeyValueWriter
	ethdb.Batcher
	ethdb.Iteratee
	ethdb.Stater
	ethdb.Compacter
	io.Closer
}

type SubstateDB struct {
	backend BackendDatabase
}

func NewSubstateDB(backend BackendDatabase) *SubstateDB {
	return &SubstateDB{backend: backend}
}

func (db *SubstateDB) Compact(start []byte, limit []byte) error {
	return db.backend.Compact(start, limit)
}

func (db *SubstateDB) Close() error {
	return db.backend.Close()
}

func CodeHash(code []byte) common.Hash {
	return crypto.Keccak256Hash(code)
}

var EmptyCodeHash = CodeHash(nil)

func (db *SubstateDB) HasCode(codeHash common.Hash) bool {
	if codeHash == EmptyCodeHash {
		return false
	}
	key := Stage1CodeKey(codeHash)
	has, err := db.backend.Has(key)
	if err != nil {
		panic(fmt.Errorf("record-replay: error checking bytecode for codeHash %s: %v", codeHash.Hex(), err))
	}
	return has
}

func (db *SubstateDB) GetCode(codeHash common.Hash) []byte {
	if codeHash == EmptyCodeHash {
		return nil
	}
	key := Stage1CodeKey(codeHash)
	code, err := db.backend.Get(key)
	if err != nil {
		panic(fmt.Errorf("record-replay: error getting code %s: %v", codeHash.Hex(), err))
	}
	return code
}

func (db *SubstateDB) PutCode(code []byte) {
	if len(code) == 0 {
		return
	}
	codeHash := crypto.Keccak256Hash(code)
	key := Stage1CodeKey(codeHash)
	err := db.backend.Put(key, code)
	if err != nil {
		panic(fmt.Errorf("record-replay: error putting code %s: %v", codeHash.Hex(), err))
	}
}

func (db *SubstateDB) HasSubstate(block uint64, tx int) bool {
	key := Stage1SubstateKey(block, tx)
	has, _ := db.backend.Has(key)
	return has
}

func (db *SubstateDB) GetSubstate(block uint64, tx int) *Substate {
	var err error

	key := Stage1SubstateKey(block, tx)
	value, err := db.backend.Get(key)
	if err != nil {
		panic(fmt.Errorf("record-replay: error getting substate %v_%v from substate DB: %v,", block, tx, err))
	}

	// try decoding as substates from latest hard forks
	substateRLP := SubstateRLP{}
	err = rlp.DecodeBytes(value, &substateRLP)

	if err != nil {
		// try decoding as legacy substates between Berlin and London hard forks
		berlinRLP := berlinSubstateRLP{}
		err = rlp.DecodeBytes(value, &berlinRLP)
		if err == nil {
			substateRLP.setBerlinRLP(&berlinRLP)
		}
	}

	if err != nil {
		// try decoding as legacy substates before Berlin hard fork
		legacyRLP := legacySubstateRLP{}
		err = rlp.DecodeBytes(value, &legacyRLP)
		if err != nil {
			panic(fmt.Errorf("error decoding substateRLP %v_%v: %v", block, tx, err))
		}
		substateRLP.setLegacyRLP(&legacyRLP)
	}

	substate := Substate{}
	substate.SetRLP(&substateRLP, db)

	return &substate
}

func (db *SubstateDB) GetBlockSubstates(block uint64) map[int]*Substate {
	var err error

	txSubstate := make(map[int]*Substate)

	prefix := Stage1SubstateBlockPrefix(block)

	iter := db.backend.NewIterator(prefix, nil)
	for iter.Next() {
		key := iter.Key()
		value := iter.Value()

		b, tx, err := DecodeStage1SubstateKey(key)
		if err != nil {
			panic(fmt.Errorf("record-replay: invalid substate key found for block %v: %v", block, err))
		}

		if block != b {
			panic(fmt.Errorf("record-replay: GetBlockSubstates(%v) iterated substates from block %v", block, b))
		}

		// try decoding as substates from latest hard forks
		substateRLP := SubstateRLP{}
		err = rlp.DecodeBytes(value, &substateRLP)

		if err != nil {
			// try decoding as legacy substates between Berlin and London hard forks
			berlinRLP := berlinSubstateRLP{}
			err = rlp.DecodeBytes(value, &berlinRLP)
			if err == nil {
				substateRLP.setBerlinRLP(&berlinRLP)
			}
		}

		if err != nil {
			// try decoding as legacy substates before Berlin hard fork
			legacyRLP := legacySubstateRLP{}
			err = rlp.DecodeBytes(value, &legacyRLP)
			if err != nil {
				panic(fmt.Errorf("error decoding substateRLP %v_%v: %v", block, tx, err))
			}
			substateRLP.setLegacyRLP(&legacyRLP)
		}

		substate := Substate{}
		substate.SetRLP(&substateRLP, db)

		txSubstate[tx] = &substate
	}
	iter.Release()
	err = iter.Error()
	if err != nil {
		panic(err)
	}

	return txSubstate
}

func (db *SubstateDB) PutSubstate(block uint64, tx int, substate *Substate) {
	var err error

	// put deployed/creation code
	for _, account := range substate.InputAlloc {
		db.PutCode(account.Code)
	}
	for _, account := range substate.OutputAlloc {
		db.PutCode(account.Code)
	}
	if msg := substate.Message; msg.To == nil {
		db.PutCode(msg.Data)
	}

	key := Stage1SubstateKey(block, tx)
	defer func() {
		if err != nil {
			panic(fmt.Errorf("record-replay: error putting substate %v_%v into substate DB: %v", block, tx, err))
		}
	}()

	substateRLP := NewSubstateRLP(substate)
	value, err := rlp.EncodeToBytes(substateRLP)
	if err != nil {
		panic(err)
	}

	err = db.backend.Put(key, value)
	if err != nil {
		panic(err)
	}
}

func (db *SubstateDB) DeleteSubstate(block uint64, tx int) {
	key := Stage1SubstateKey(block, tx)
	err := db.backend.Delete(key)
	if err != nil {
		panic(err)
	}
}

type Transaction struct {
	Block       uint64
	Transaction int
	Substate    *Substate
}

type rawEntry struct {
	key   []byte
	value []byte
}

func parseTransaction(db *SubstateDB, data rawEntry) *Transaction {
	key := data.key
	value := data.value

	block, tx, err := DecodeStage1SubstateKey(data.key)
	if err != nil {
		panic(fmt.Errorf("record-replay: invalid substate key found: %v - issue: %v", key, err))
	}

	// try decoding as substates from latest hard forks
	substateRLP := SubstateRLP{}
	err = rlp.DecodeBytes(value, &substateRLP)

	if err != nil {
		// try decoding as legacy substates between Berlin and London hard forks
		berlinRLP := berlinSubstateRLP{}
		err = rlp.DecodeBytes(value, &berlinRLP)
		if err == nil {
			substateRLP.setBerlinRLP(&berlinRLP)
		}
	}

	if err != nil {
		// try decoding as legacy substates before Berlin hard fork
		legacyRLP := legacySubstateRLP{}
		err = rlp.DecodeBytes(value, &legacyRLP)
		if err != nil {
			panic(fmt.Errorf("error decoding substateRLP %v_%v: %v", block, tx, err))
		}
		substateRLP.setLegacyRLP(&legacyRLP)
	}

	substate := &Substate{}
	substate.SetRLP(&substateRLP, db)

	return &Transaction{
		Block:       block,
		Transaction: tx,
		Substate:    substate,
	}
}

type SubstateIterator struct {
	db   *SubstateDB
	iter ethdb.Iterator
	cur  *Transaction

	// Connections to parsing pipeline
	source <-chan *Transaction
	done   chan<- int
}

func NewSubstateIterator(start_block uint64, num_workers int) SubstateIterator {
	db := staticSubstateDB
	start := Stage1SubstateBlockPrefix(start_block)
	iter := db.backend.NewIterator(nil, start)

	// Create channels
	done := make(chan int)
	raw_data := make([]chan rawEntry, num_workers)
	results := make([]chan *Transaction, num_workers)
	result := make(chan *Transaction, 10)

	for i := 0; i < num_workers; i++ {
		raw_data[i] = make(chan rawEntry, 10)
		results[i] = make(chan *Transaction, 10)
	}

	// Start iter => raw data stage
	go func() {
		defer func() {
			for _, c := range raw_data {
				close(c)
			}
		}()
		step := 0
		for {
			if !iter.Next() {
				return
			}

			key := make([]byte, len(iter.Key()))
			copy(key, iter.Key())
			value := make([]byte, len(iter.Value()))
			copy(value, iter.Value())

			res := rawEntry{key, value}

			select {
			case <-done:
				return
			case raw_data[step] <- res: // fall-through
			}
			step = (step + 1) % num_workers
		}
	}()

	// Start raw data => parsed transaction stage (parallel)
	for i := 0; i < num_workers; i++ {
		id := i
		go func() {
			defer close(results[id])
			for raw := range raw_data[id] {
				results[id] <- parseTransaction(db, raw)
			}
		}()
	}

	// Start the go routine moving transactions from parsers to sink in order
	go func() {
		defer close(result)
		step := 0
		for open_producers := num_workers; open_producers > 0; {
			next := <-results[step%num_workers]
			if next != nil {
				result <- next
			} else {
				open_producers--
			}
			step++
		}
	}()

	return SubstateIterator{
		db:     db,
		iter:   iter,
		source: result,
		done:   done,
	}
}

func (i *SubstateIterator) Release() {
	close(i.done)

	// drain pipeline until the result channel is closed
	for open := true; open; _, open = <-i.source {
	}

	i.iter.Release()
}

func (i *SubstateIterator) Next() bool {
	if i.iter == nil {
		return false
	}
	i.cur = <-i.source
	return i.cur != nil
}

func (i *SubstateIterator) Value() *Transaction {
	return i.cur
}
