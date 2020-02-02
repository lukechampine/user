# user

`user` is a CLI renter for Sia. It is an alternative to `siad`'s renter module.
The biggest difference is that `user` is a program that you invoke to perform
specific actions, whereas `siad` is a daemon that runs continuously in the
background. The other major difference is that `siad` manages your contracts for
you by select good hosts, maintaining a pool of usuable contracts, and
automatically renewing contracts when necessary. `user`, by contrast, offloads
these responsibilities to a [`muse`](https://github.com/lukechampine/muse)
server.

`user` provides some functionality that `siad` does not. It allows you to upload
and download with having to run a full node; it efficiently stores small files;
it makes it easy to share files with your friends; and more. On the other hand,
since `user` does not run in the background, it cannot automatically repair your
files like `siad` does.


## Setup

First you'll need to install `user` by checking out this repository and running
`make`. Run `user version` to confirm that it's installed.

`user` cannot operate on its own; it needs contracts, which it gets from a
[`muse`](https://github.com/lukechampine/muse) server. You can specify the
address of this server using a CLI flag, but it's more convenient to add it to
your [config file](#configuration).


## Uploading and Downloading Files

`user` stores and retrieves files using *metafiles*, which are small files
containing the metadata necessary to retrieve and modify a file stored on a
host. Uploading a file creates a metafile, and downloading a metafile creates a
file. Metafiles can be downloaded by anyone possessing contracts with the file's
hosts. You can share a metafile simply by sending it; to share multiple files,
bundle their corresponding metafiles in an archive such as a `.tar` or `.zip`.

Note that metafiles represent "snapshots" of a file at a particular time; if you
share a metafile, and then modify your copy, the recipient will not see your
modifications. Likewise, if the recipient modifies their copy, it will not
affect your own.

The upload and download commands are straightforward:

```
$ user upload [file] [metafile]

$ user download [metafile] [file]
```

`file` is the path of the file to be read (during upload) or written (during
download), and `metafile` is the path where the file metadata will be written.
The extension for metafiles is `.usa` (`a` for "archive"). If you omit the final
argument, the name of the file or metafile is chosen automatically (by either
removing or appending the `.usa` extension).

When uploading, you must specify the desired redundancy of the file, which you
can do by passing the `-m` flag or by setting the `min_shards` value in your
[config file](#configuration). This value refers to the minimum number of hosts
that must be reachable for you to download the file. For example, if you have 10
contracts, and you upload with `-m 5`, you will be able to download as long as
any 5 hosts are reachable. The redundancy of the file in this example is 2x.

The `upload` command erasure-encodes `file` into "shards," encrypts each shard
with a different key, and uploads one shard to each host. The `download` command
is the inverse: it downloads shards from each host, decrypts them, and joins the
erasure-encoded shards back together, writing the result to `file`.

Uploads and downloads are resumable. If `metafile` already exists when starting
an upload, or if `file` is smaller than the target filesize when starting a
download, then these commands will pick up where they left off.

You can also upload or download multiple files by specifying a directory path
for both `file` and `metafile`. The directory structure of the metafiles will
mirror the structure of the files. This variant is strongly recommended when
uploading many small files, because it allows `user` to pack multiple files
into a single 4MB sector, which saves lots of bandwidth and money. (Normally,
each uploaded file must be padded to 4MB.)

It is also possible to redirect a download command:

```
$ user download [metafile] | wc -l
```

This means you can pipe downloaded files directly into other commands without
creating a temporary file.


## Migrating Files

When metafiles have a redundancy greater than 1x, they can still be downloaded
even if some of their hosts are unreachable. But if too many hosts become
unreachable, the metafile will be lost. For this reason, it is prudent to
re-upload your metafiles to better hosts if they are at risk of being lost.
In `us`, this process is called "migration."

If you have a local copy of the original file, you can re-upload it to the new
hosts immediately. If you don't have a local copy, you must download the file
first, then re-upload it to the new hosts. In `user`, these options are called
`file` and `remote`, respectively.

Let's assume that you uploaded a file to three hosts with `min_shards = 2`, and
one of them is now unresponsive. You would like to repair the missing redundancy
by migrating the shard on the unresponsive host to a new host. If you had a copy
of the original file, you could run:

```
$ user migrate -file=[file] [metafile]
```

Unfortunately, in this example, you do not have the original file. However,
there are still two good hosts available, so you can download their shards and
use them to reconstruct the third shard by running:

```
$ user migrate -remote [metafile]
```

Note that in a remote migration, the file is not actually downloaded to disk; it
is processed piecewise in RAM. You don't need any free disk space to perform a
migration.

Like uploads and downloads, migrations can be resumed if interrupted, and can
also be applied to directories.


## Configuration

`user` can be configured via a file named `~/.config/user/config.toml`:

```toml
# API address of muse server.
# REQUIRED.
muse_addr = "muse.lukechampine.com/<my-muse-id>"

# API address of SHARD server.
# OPTIONAL. If not provided, the muse server will be used instead.
shard_addr = "shard.lukechampine.com"

# Minimum number of hosts required to download a file. Also controls
# file redundancy: uploading to 40 hosts with min_shards = 10 results
# in 4x redundancy.
# REQUIRED (unless the -m flag is passed to user).
min_shards = 10
```


## Extras

### Uploading and Downloading with FUSE

FUSE is a technology that allows you to mount a "virtual filesystem" on your
computer. The `user` FUSE filesystem behaves like a normal folder, but behind
the scenes, it is transferring data to and from Sia hosts. You can upload a file
simply by copying it into the folder, or download by opening a file within the
folder.

The command to mount the virtual filesystem is:

```
$ user mount [metadir] [mnt]
```

`metadir` is the directory where metafiles will be written and read. Each such
metafile will correspond to a virtual file in the `mnt` directory. For example,
if you create `bar/foo.txt` in `mnt`, then `bar/foo.txt.usa` will appear in
`metadir`.

Unlike most `user` commands, `mount` will remain running until you stop it with
Ctrl-C. Don't kill it suddenly (e.g. by turning off your computer) or you will
almost certainly lose data. If you do experience an unclean shutdown, you may
encounter errors accessing the folder later. To fix this, run `fusermount -u`
on the `mnt` directory to forcibly unmount it.


### Downloading over HTTP

`user` can serve a directory of metafiles over HTTP with the `serve` command:

```
$ user serve [metadir]
```

You can then browse to http://localhost:8080 to view the files in your web
browser.
