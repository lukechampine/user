# user

`user` is a CLI renter for Sia. It is an alternative to `siad`'s renter module.
The biggest difference is that `user` is a program that you invoke to perform
specific actions, whereas `siad` is a daemon that runs continuously in the
background. `siad` does a lot of work for you, including selecting good hosts,
automatically renewing your contracts, and "repairing" your files when their
hosts go offline. `user` cannot do these things; it can only do what you
explicitly tell it to do. It's up to you to select good hosts, renew your
contracts before they expire, and migrate to new hosts if the old hosts flake
out. **Failure to perform these duties can result in loss of data.**

On the bright side, `user` enables you to do things that aren't possible with
`siad`. You have precise control over which hosts you use and how many siacoins
you spend; you can share your files and contracts with a friend; you can upload
and download without a full node; you can efficiently store small files; you can
mount a virtual Sia filesystem with FUSE; and more. In short, `user` is a tool
for developers and power users who want to interact with Sia at a lower level
and in new, exciting ways.


## Setup

First you'll need to install `user` by running this command:

```
$ go get lukechampine.com/user
```

Alternatively, checkout this repository and run `make`.

`user` cannot operate on its own; it needs to talk to another server to form
contracts, resolve host addresses, and learn the current block height. This
guide assumes that the server is `siad`. Later sections describe how to use
`user` with [alternative](#using-a-shard-server) [servers](#using-a-walrus-server).

You will need a synchronized `siad` with an unlocked wallet. The wallet should
contain enough siacoins to fund the contracts you want to form. If your instance
of `siad` is using a non-standard API port or password, refer to
[Configuration](#configuration) to set those values accordingly.


## Scanning for Hosts

Before you can store files, you need to form contracts, and before you can form
contracts, you need to choose which hosts you'll use. You can get a ranked list
of hosts by running `siac hostdb -v`. The longer `siad` has been running, the
more accurate these rankings will be. You can also consult a service like
[SiaStats](https://siastats.info/hosts), which uses `us` to regularly benchmark
hosts and measure their true performance.

You'll need the public key of each host you want to use. Host public keys look
like this:

```
ed25519:706715a4f37fda29f8e06b867c5df3f139f6ed93c18d99a5665eb66a5fab6009
```

Since these keys are long and unwieldy, `user` lets you use an abbreviated form.
In the abbreviated form, the `ed25519:` prefix is dropped, and only the first
few characters of the key are retained. The key above, for example, could be
shortened to `706715a4`. Like git hashes, you only need enough characters to
ensure that the key is unambiguous; eight is a safe choice.


## Forming Contracts

Now we're ready to form a contract. The command syntax is:

```
$ user form [hostkey] [funds] [endheight] [contract]
```

`hostkey` is the public key of the host; `funds` is the amount of siacoins the
contract will store; `endheight` is the height at which the host is no longer
obligated to store the data; and `contract` is the filepath where the contract
itself will be written. If `contract` is not supplied, the contract will be
written to the default contract directory. (See [Configuration](#configuration).)

`user` manages contracts in a way that should be familiar to users of
[NGINX](https://www.nginx.com). All contracts are stored as files in a directory
(typically named `contracts-available`), and to *enable* a contract, you create
a symlink to it in another directory (typically named `contracts-enabled`). When
you form a contract with `user form`, `user` will enable the contract for you by
creating the appropriate symlink.

Note that, in the above command, `funds` does not include the transaction fee,
the host's contract fee, or the siafund tax. `funds` is simply the number of
coins in the renter's half of the payment channel, i.e. the amount reserved for
paying the host when uploading and downloading. In practice, it is difficult to
predict exactly how much a contract will cost, but `user` provides a command
that can give a reasonably-accurate estimate:

```
$ user scan [hostkey] [filesize] [duration] [downloads]
Data Cost:       1000 SC
Host Fee:         200 SC
Siafund Fee:      100 SC
Transaction Fee:   10 SC
Total:           1310 SC
```

`filesize` is the total amount of data stored, `duration` is the number of
blocks the data is stored for, and `downloads` is the expected number of times
the data will be downloaded. The `Data Cost` field indicates how many siacoins
should be specified when calling `form`, and the `Total` field estimates how
many coins will be spent from the wallet when `form` is called.


## Renewing Contracts

Once you have a contract, renewing is easy:

```
$ user renew [contract] [funds] [endheight] [newcontract]
```

`contract` is the path of the original contract metadata file, and
`newcontract` is where the new contract metadata will be written. If
`newcontract` is not supplied, the new contract will be written to a file
named according to the same scheme as `user form`.

When a contract is renewed, the new contract is automatically enabled and the
old contract is disabled (if applicable). Lastly, a suffix (`_old`) is appended
to the filename of the old contract to ensure that it will no longer be used.

The host may be offline when you attempt to renew, in which case you will have
to try again later. If the contract is not renewed before it expires, the host
will delete any data associated with the contract. For this reason, it is
recommended that you first attempt to renew a contract at least 1000 blocks
(approx. 1 week) before it expires.


## Uploading and Downloading Files

`user` stores and retrieves files using *metafiles*, which are small files
containing the metadata necessary to retrieve and update a file stored on a
host. Uploading a file creates a metafile, and downloading a metafile creates a
file. Metafiles can be downloaded by anyone possessing contracts with the file's
hosts. You can share a metafile simply by sending it; to share multiple files,
bundle their corresponding metafiles in an archive such as a `.tar` or `.zip`.

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

The `upload` command splits `file` into "shards," encrypts each shard with a
different key, and uploads one shard to each host. The `download` command is the
inverse: it downloads shards from each host, decrypts them, and joins the
erasure-coded shards back together, writing the result to `file`.

Uploads and downloads are resumable. If `metafile` already exists during
upload, or if `file` is smaller than the target filesize during download, then
these commands will pick up where they left off.

You can also upload or download multiple files by specifying a directory path
for both `file` and `metafile`. The directory structure of the metafiles will
mirror the structure of the files. This variant is strongly recommended when
uploading many small files, because it allows `user` to pack multiple files
into a single 4MB sector, which saves lots of bandwidth and money. (Normally,
each uploaded file must be padded to 4MB.)

It is also possible to redirect a download command:

```
$ user download [metafile] > myfile
```

This means you can pipe downloaded files directly into other commands without
creating a temporary file.


## Blacklisting Hosts

Sia's design assumes that hosts may fail or provide poor quality of service.
If a host goes offline, transfers data too slowly, raises their prices too
high, etc., naturally we would like to blacklist them. This is as simple as:

```
$ user contracts disable [hostkey]
```

Disabling a contract does not delete it permanently; the actual contract file
remains in the `contracts_available` directory. This command simply removes
the corresponding symlink in the `contracts_enabled` directory.

Of course, if you blacklist too many hosts, you may not be able to download
your files from the remaining set. To re-enable a contract, run:

```
$ user contracts enable [hostkey]
```

As expected, this command simply recreates a symlink in the `contracts-enabled`
directory.

The use of symlinks allows you to create multiple sets of enabled contracts
and quickly switch between them. For example, you could have a directory
called `contracts-cheap` that references the cheapest hosts, and another
directory called `contracts-fast` that references the fastest hosts. You can
then pass the `-c` flag to `user` to switch between these sets at will.


## Migrating Files

Blacklisting hosts will improve your quality of service, but it also reduces
the redundancy of your files. In the long-term, it is safest to re-upload your
data to better hosts. In `us`, this process is called "migration."

There are three ways to migrate a file, depending on how you obtain the data
that will be uploaded to the new hosts. If you have a copy of the original file,
you can simply use that data. Alternatively, if you are able and willing to
download from the bad hosts, you can get the data from them. Finally, if you
don't have a copy of the file and the bad hosts are offline, too expensive, or
too slow, you can download from just the good hosts and then reconstruct the
missing redundancy. In `user`, these options are called `file`, `direct`, and
`remote`, respectively. `file` is the cheapest and fastest option; `remote` is
generally the slowest and most expensive, but is often the only choice; and
`direct` may be better or worse than `remote` depending on the quality of the
bad hosts.

Let's assume that you uploaded a file to three hosts with `min_shards = 2`,
and one of them is now unresponsive. You would like to repair the missing
redundancy by migrating the shard on the unresponsive host to a new host.
First, if you haven't already done so, blacklist the old host by running:

```
$ user contracts disable [hostkey]
```

Next, form a new contract with the new host. (The new contract will be enabled
automatically.) Now, you can perform the actual migration. If you had a copy
of the original file, you could run:

```
$ user migrate -file=[file] [metafile]
```

Unfortunately, in this example, you do not have the original file.

If the old host were not unresponsive, you could run:

```
$ user migrate -direct [metafile]
```

Unfortunately, in this example, the host is unresponsive.

However, there are two good hosts available, so you can download their shards
and use them to reconstruct the third shard by running:

```
$ user migrate -remote [metafile]
```

All three migration options can be resumed if interrupted, and can also be
applied to directories.


## Configuration

`user` can be configured via a file named `~/.config/user/config.toml`. The
name, description, and default value of each setting is given in the below
example file:

```toml
# API port of siad.
# OPTIONAL. Default: "localhost:9980"
siad_addr = "localhost:1993"

# API password of siad. If not defined, user will attempt to read the standard
# siad password file, ~/.sia/apipassword.
# OPTIONAL. Default: ""
siad_password = "foobarbaz"

# Directory where contracts are stored. An absolute path is recommended.
# OPTIONAL. Default: "~/.config/us/contracts-available"
contracts_available = "~/us/available"

# Directory where enabled contracts are stored. This directory should contain
# only symlinks to the contracts folder. An absolute path is recommended.
# OPTIONAL. Default: "~/.config/us/contracts-enabled"
contracts_enabled = "~/us/enabled"

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


### Using a `shard` Server

`user` can talk to a [`shard`](https://github.com/lukechampine/shard) server to
learn the current blockheight and resolve host addresses. Forming and renewing
contracts still requires `siad`, but once you have contracts, a `shard` server
allows you to upload and download without a full node.

To configure `user` to talk to a `shard` server, simply add its address to your
`config.toml`, e.g.:

```toml
shard_addr = "12.34.56.78"
```

`user` will then use the `shard` server when it can, and `siad` otherwise.


### Using a `walrus` Server

`user` can talk to a `walrus` server to form and renew contracts. If you
configure `user` to use a `walrus` server, you must use a `shard` server as
well, because `walrus` cannot perform host address resolution.

To configure `user` to talk to a `walrus` server, simply add its address to your
`config.toml`, e.g.:

```toml
walrus_addr = "12.34.56.78:9999"
```

`user` will then use the `walrus` and `shard` servers instead of `siad`.
