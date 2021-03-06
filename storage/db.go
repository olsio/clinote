/*
 * This program is free software; you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation; either version 2 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program; if not, see <http://www.gnu.org/licenses/>.
 *
 * Copyright (C) Joakim Kennedy, 2018
 */

package storage

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/TcM1911/clinote"
	"github.com/boltdb/bolt"
)

const (
	dbFilename = "clinote.db"
)

// 0: Initial version of the database.
// 1: Added credential store, migration of OAuth token.
var softwareDBVersion = uint64(1)

// This is what the current wait time before the database is closed.
var currentWaitTime = 5 * time.Second

// List of buckets
var (
	dbBucket       = []byte("db_data")
	settingsBucket = []byte("settings")
	cacheBucket    = []byte("cache")
)

// List of keys
var (
	settingsKey         = []byte("user_settings")
	credentialsKey      = []byte("user_credentials")
	notebookCacheKey    = []byte("notebook_cache")
	searchCacheKey      = []byte("note_search_cache")
	noteRecoverCacheKey = []byte("note_recover_cache")
	dbVersionKey        = []byte("dbVersion")
)

var (
	errNoBucket = errors.New("no bucket")
	// ErrIndexOutOfRange is returned if an index is out of range.
	ErrIndexOutOfRange = errors.New("index out of range")
	// ErrEncodeDBVersion is returned if the database version fails to be encoded.
	ErrEncodeDBVersion = errors.New("failed to encode db version")
)

// Open returns an instance of the database.
func Open(cfgFolder string) (*Database, error) {
	filename := filepath.Join(cfgFolder, dbFilename)
	b, err := bolt.Open(filename, 0600, nil)
	if err != nil {
		return nil, err
	}
	d := &Database{
		bolt:       b,
		dbFilename: filename,
		resetChan:  make(chan struct{}, 1),
		// TODO: This property should be configurable.
		waitTime: currentWaitTime,
	}
	go dbWaitingLoop(d)

	// Check if migration is needed.
	currVersion, err := d.getDBVersion()
	if err != nil {
		return nil, err
	}
	if currVersion < softwareDBVersion {
		err = migrate(d, currVersion)
		if err != nil {
			return nil, err
		}
		err = d.saveDBVersion(softwareDBVersion)
	}
	return d, err
}

func dbWaitingLoop(d *Database) {
	defer d.closeDB()
	for {
		for {
			select {
			case <-d.resetChan:
				break

			case <-time.After(d.waitTime):
				return
			}
		}
	}
}

// Database is a representation of the backend storage.
type Database struct {
	// bolt is the database handler. This should not be accessed directly.
	// Instead getDBHandler() should be used to get a handler. Accessing this directly
	// is not thread safe.
	bolt *bolt.DB
	// Mutex for the handler to ensure only one go routine has the handler.
	handlerMu sync.Mutex

	// dbFilename is the absolute path of the database file.
	dbFilename string
	// resetChan is used to reset the timer before the database is closed.
	resetChan chan struct{}
	// waitTime is how long the database should be held open.
	waitTime time.Duration
}

// open is used internally to reopen the database file. This method is not thread safe and
// assumes the caller holds the lock.
func (d *Database) open() (*bolt.DB, error) {
	if d.bolt != nil {
		return d.bolt, nil
	}
	// Start closing wait loop
	go dbWaitingLoop(d)
	return bolt.Open(d.dbFilename, 0600, nil)
}

func (d *Database) closeDB() error {
	d.handlerMu.Lock()
	defer d.handlerMu.Unlock()

	if d.bolt == nil {
		return nil
	}

	err := d.bolt.Close()
	d.bolt = nil
	return err
}

// getDBHandler returns a handler to the database after it has acquired the mutex lock.
// The caller MUST call releaseDBHandler when done. Otherwise this can cause a deadlock.
func (d *Database) getDBHandler() (*bolt.DB, error) {
	d.handlerMu.Lock()
	if d.bolt != nil {
		// Wrap the call in a go func to prevent deadlock.
		// TODO: Wait-loop should be rewritten so this is not needed.
		go func() {
			// Reset timer
			d.resetChan <- struct{}{}
		}()
		return d.bolt, nil
	}
	b, err := d.open()
	if err != nil {
		return nil, err
	}
	d.bolt = b

	return d.bolt, nil
}

// releaseDBHandler unlocks the mutex to get other go routines access to the database handler.
func (d *Database) releaseDBHandler() {
	d.handlerMu.Unlock()
}

func (d *Database) getDBVersion() (uint64, error) {
	data, err := d.getData(dbBucket, dbVersionKey)
	if err != nil {
		return uint64(0), err
	}
	version, n := binary.Uvarint(data)
	if n == 0 {
		return uint64(0), nil
	}
	return version, nil
}

func (d *Database) saveDBVersion(version uint64) error {
	buf := make([]byte, 8)
	n := binary.PutUvarint(buf, version)
	if n <= 0 {
		return ErrEncodeDBVersion
	}
	return d.storeData(dbBucket, dbVersionKey, buf)
}

func (d *Database) getData(bucket, key []byte) ([]byte, error) {
	var data []byte
	db, err := d.getDBHandler()
	defer d.releaseDBHandler()
	if err != nil {
		return data, err
	}
	err = db.View(func(t *bolt.Tx) error {
		b := t.Bucket(bucket)
		if b == nil {
			return errNoBucket
		}
		data = b.Get(key)
		return nil
	})
	if err == errNoBucket {
		err := db.Update(func(t *bolt.Tx) error {
			_, err := t.CreateBucket(bucket)
			if err != nil {
				return err
			}
			return nil
		})
		return data, err
	}
	return data, err
}

func (d *Database) storeData(bucket, key, data []byte) error {
	db, err := d.getDBHandler()
	defer d.releaseDBHandler()
	if err != nil {
		return err
	}
	return db.Update(func(t *bolt.Tx) error {
		b, err := t.CreateBucketIfNotExists(bucket)
		if err != nil {
			return err
		}
		return b.Put(key, data)
	})
}

// GetSettings returns the settings from the database.
func (d *Database) GetSettings() (*clinote.Settings, error) {
	var settings clinote.Settings
	data, err := d.getData(settingsBucket, settingsKey)
	if err == nil && data != nil {
		err = json.Unmarshal(data, &settings)
	}
	return &settings, err
}

// StoreSettings saves the settings to the database.
func (d *Database) StoreSettings(settings *clinote.Settings) error {
	data, err := json.Marshal(settings)
	if err != nil {
		return err
	}
	return d.storeData(settingsBucket, settingsKey, data)
}

// GetNotebookCache returns the stored NotebookCacheList.
func (d *Database) GetNotebookCache() (*clinote.NotebookCacheList, error) {
	var list clinote.NotebookCacheList
	data, err := d.getData(cacheBucket, notebookCacheKey)
	if err == nil && data != nil {
		err = json.Unmarshal(data, &list)
	}
	return &list, err
}

// StoreNotebookList saves the list to the database.
func (d *Database) StoreNotebookList(list *clinote.NotebookCacheList) error {
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return d.storeData(cacheBucket, notebookCacheKey, data)
}

// SaveSearch stores the search to the database.
func (d *Database) SaveSearch(notes []*clinote.Note) error {
	data, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	return d.storeData(cacheBucket, searchCacheKey, data)
}

// GetSearch gets the saved search from the database.
func (d *Database) GetSearch() ([]*clinote.Note, error) {
	var notes []*clinote.Note
	data, err := d.getData(cacheBucket, searchCacheKey)
	if err == nil && data != nil {
		err = json.Unmarshal(data, &notes)
	}
	return notes, err
}

// SaveNoteRecoveryPoint saves the note to the database so it can be
// recovered in the case something fails.
func (d *Database) SaveNoteRecoveryPoint(note *clinote.Note) error {
	data, err := json.Marshal(note)
	if err != nil {
		return err
	}
	return d.storeData(cacheBucket, noteRecoverCacheKey, data)
}

// GetNoteRecoveryPoint returns the saved note that failed to save.
func (d *Database) GetNoteRecoveryPoint() (*clinote.Note, error) {
	var note clinote.Note
	data, err := d.getData(cacheBucket, noteRecoverCacheKey)
	if err == nil && data != nil {
		err = json.Unmarshal(data, &note)
	}
	return &note, err
}

// Close shuts down the connection to the database.
func (d *Database) Close() error {
	return d.closeDB()
}

// Add adds a new credential to the database.
func (d *Database) Add(c *clinote.Credential) error {
	creds, err := d.GetAll()
	if err != nil {
		return err
	}
	creds = append(creds, c)
	data, err := json.Marshal(creds)
	if err != nil {
		return err
	}
	return d.storeData(settingsBucket, credentialsKey, data)
}

// Remove removes the credential from the database.
func (d *Database) Remove(c *clinote.Credential) error {
	credList, err := d.GetAll()
	if err != nil {
		return err
	}
	index := -1
	for i := 0; i < len(credList); i++ {
		if *credList[i] == *c {
			index = i
			break
		}
	}
	if index == -1 {
		return clinote.ErrNoMatchingCredentialFound
	}
	// Remove the entry
	copy(credList[index:], credList[index+1:])
	credList[len(credList)-1] = nil
	credList = credList[:len(credList)-1]

	data, err := json.Marshal(credList)
	if err != nil {
		return err
	}
	// Save the list
	return d.storeData(settingsBucket, credentialsKey, data)
}

// GetAll returns all the credentials in the database.
func (d *Database) GetAll() ([]*clinote.Credential, error) {
	var creds []*clinote.Credential
	data, err := d.getData(settingsBucket, credentialsKey)
	if err == nil && data != nil {
		err = json.Unmarshal(data, &creds)
	}
	return creds, err
}

// GetByIndex returns a credential by its index.
func (d *Database) GetByIndex(index int) (*clinote.Credential, error) {
	creds, err := d.GetAll()
	if err != nil {
		return nil, err
	}
	if index < 0 || index >= len(creds) {
		return nil, ErrIndexOutOfRange
	}
	return creds[index], nil
}
