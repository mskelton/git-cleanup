#!/usr/bin/env bash

set -eu

package=git-cleanup
platforms=(
	"windows/amd64"
	"windows/arm64"
	"darwin/amd64"
	"darwin/arm64"
	"linux/amd64"
	"linux/arm64"
)

for platform in "${platforms[@]}"; do
	echo "Building for $platform"

	platform_split=(${platform//\// })
	GOOS=${platform_split[0]}
	GOARCH=${platform_split[1]}

	bin=$package
	if [ $GOOS = "windows" ]; then
		bin+='.exe'
	fi

	# Build the binary
	env GOOS=$GOOS GOARCH=$GOARCH go build -o $bin .

	# Zip the binary
	mkdir -p dist
	output_name="$package-$GOOS-$GOARCH"

	if [ $GOOS = "windows" ]; then
		output_name="$output_name.zip"
		7z a dist/$output_name $bin
	else
		output_name="$output_name.tar.gz"
		tar czf dist/$output_name $bin
	fi

	if [ $? -ne 0 ]; then
		echo 'An error has occurred! Aborting the script execution...'
		exit 1
	fi
done
