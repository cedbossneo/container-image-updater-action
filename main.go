package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"

	"github.com/sirupsen/logrus"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/estesp/manifest-tool/v2/pkg/registry"
	"github.com/estesp/manifest-tool/v2/pkg/store"
	"github.com/estesp/manifest-tool/v2/pkg/types"
	"github.com/estesp/manifest-tool/v2/pkg/util"
)

var (
	baseImage, image                                     string
	baseImageRegistryUsername, baseImageRegistryPassword string
	imageRegistryUsername, imageRegistryPassword         string
	platform                                             string
)

func init() {
	flag.StringVar(&baseImage, "base-image", "", "Base Image")
	flag.StringVar(&image, "image", "", "Image")
	flag.StringVar(&platform, "platform", "", "Platform")
	flag.StringVar(&baseImageRegistryUsername, "base-reg-username", "", "Base Image Registry Username")
	flag.StringVar(&baseImageRegistryPassword, "base-reg-password", "", "Base Image Registry Password")
	flag.StringVar(&imageRegistryUsername, "image-reg-username", "", "Image Registry Username")
	flag.StringVar(&imageRegistryPassword, "image-reg-password", "", "Image Registry Password")
	flag.Parse()
}

func main() {
	if baseImage == "" || image == "" {
		fmt.Println("::error ::baseImage and image should be set")
		return
	}
	baseLayers, err := parseImage(baseImage, baseImageRegistryUsername, baseImageRegistryPassword)
	if err != nil {
		fmt.Printf("::error ::failed to get layers for the base image, err: %v\n", err)
		return
	}
	imageLayers, err := parseImage(image, imageRegistryUsername, imageRegistryPassword)
	if err != nil {
		fmt.Printf("::error ::failed to get layers for the image, err: %v\n", err)
		return
	}
	for _, imageLayer := range imageLayers {
		found := false
		for _, baseLayer := range baseLayers {
			found = subset(baseLayer, imageLayer)
			if found {
				break
			}
		}
		if !found {
			ioutil.WriteFile(os.Getenv("GITHUB_STATE"), []byte("needs-update=true\n"), fs.ModeAppend)
			return
		}
	}
	ioutil.WriteFile(os.Getenv("GITHUB_STATE"), []byte("needs-update=false\n"), fs.ModeAppend)
}

func subset(a, b []digest.Digest) bool {
	if len(a) > len(b) {
		return false
	}
	for i, l := range a {
		if l != b[i] {
			return false
		}
	}
	return true
}

func parseImage(name, username, password string) (digests [][]digest.Digest, err error) {
	resolver := util.NewResolver(username, password, false,
		false, "")
	memoryStore := store.NewMemoryStore()
	imageRef, err := util.ParseName(name)
	if err != nil {
		logrus.Fatal(err)
	}
	descriptor, err := registry.FetchDescriptor(resolver, memoryStore, imageRef)
	if err != nil {
		return nil, err
	}

	_, db, _ := memoryStore.Get(descriptor)
	switch descriptor.MediaType {
	case ocispec.MediaTypeImageIndex, types.MediaTypeDockerSchema2ManifestList:
		// this is a multi-platform image descriptor; marshal to Index type
		var idx ocispec.Index
		if err := json.Unmarshal(db, &idx); err != nil {
			return nil, err
		}
		digests, err = parseList(memoryStore, idx)
		if err != nil {
			return nil, fmt.Errorf("failed to parse the manifest list: %w", err)
		}
	case ocispec.MediaTypeImageManifest, types.MediaTypeDockerSchema2Manifest:
		var man ocispec.Manifest
		if err := json.Unmarshal(db, &man); err != nil {
			return nil, err
		}
		_, cb, _ := memoryStore.Get(man.Config)
		var conf ocispec.Image
		if err := json.Unmarshal(cb, &conf); err != nil {
			return nil, err
		}
		dig := getDigests(man.Layers)
		digests = append(digests, dig)
	default:
		return nil, fmt.Errorf("unknown descriptor type: %s", descriptor.MediaType)
	}
	return
}

func parseList(cs *store.MemoryStore, index ocispec.Index) (digests [][]digest.Digest, err error) {
	for _, img := range index.Manifests {
		_, db, _ := cs.Get(img)
		if platform != "" && img.Platform != nil && img.Platform.OS+"/"+img.Platform.Architecture != platform {
			continue
		}
		switch img.MediaType {
		case ocispec.MediaTypeImageManifest, types.MediaTypeDockerSchema2Manifest:
			var man ocispec.Manifest
			if err := json.Unmarshal(db, &man); err != nil {
				return nil, err
			}

			dig := getDigests(man.Layers)
			digests = append(digests, dig)
		default:
			return nil, fmt.Errorf("Unknown media type for further display: %s\n", img.MediaType)
		}
	}
	if len(digests) == 0 {
		return nil, fmt.Errorf("no matching platform found")
	}
	return digests, nil
}

func getDigests(layers []ocispec.Descriptor) (digests []digest.Digest) {
	for _, layer := range layers {
		digests = append(digests, layer.Digest)
	}
	return digests
}
