// Package spineparser serializes and deserializes Spine project, skeleton
// binary, and JSON files.
//
// Spine Editor project schemas are private and version-dependent. The package
// preserves raw-DEFLATE payloads losslessly and provides bounded, fail-closed
// direct project edits without invoking Spine Editor.
package spineparser
