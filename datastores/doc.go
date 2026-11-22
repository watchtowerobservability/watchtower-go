// Package datastores is the home of the embedded store integrations (one package per store:
// bbolt, badger, pebble, leveldb, buntdb, ristretto). It is its own module so the root SDK module
// does not carry their dependencies. The shared model is the root module's embedded package.
package datastores
