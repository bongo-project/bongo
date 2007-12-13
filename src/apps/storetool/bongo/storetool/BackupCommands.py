import logging
import sys

from gettext import gettext as _
import tarfile

from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from bongo.store.StoreClient import DocTypes, StoreClient

# import/export store contents as tar files
# ideally we want to farm the tar format stuff itself out into a separate lib.
# Sadly, the TarFile implementation that comes with Python is mostly useless 

class StoreBackupCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "store-backup", aliases=["sb"],
                         summary="Create a backup of a user store",
                         usage="%prog %cmd <store> [<output>]")

    def BackupStore(self, store, backup_file):
        # create a backup file which contains all the contents of the given store
        collections = [collection.name for collection in store.Collections()]
        for collection in collections:
            contents = [document for document in store.List(collection)]
            # write out an entry for the collection (it might be empty)
            tar_dir = tarfile.TarInfo(collection)
            tar_dir.size = 0
            tar_dir.type = tarfile.DIRTYPE
            backup_file.write(tar_dir.tobuf())

            for document in contents:
                # write out the documents in the collection
                if document.filename[0] == '/':
                    # this is a collection
                    continue
                filename = "%s/%s" % (collection, document.filename)
                try:
                    print "Reading %s" % filename
                    content = store.Read(filename)
                except IOError, e:
                    print str(e)
                    continue

                # tar files have a 512 byte header (tar_info.tobuf()), and then the content
                # content is padded to a multiple of 512 bytes
                tar_info = tarfile.TarInfo(filename)
                tar_info.size = len(content)
                backup_file.write(tar_info.tobuf())
                backup_file.write(content)
                space = len(content) % 512
                if space != 0:
                    backup_file.write("\0" * (512 - space))

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for storename in args:
                backup_file = "%s.backup" % storename
                try:
                    f = open(backup_file, "wb")
                    store.Store(storename)
                    self.BackupStore(store, f)
                except IOError, e:
                    print str(e)
                finally:
                    if f:
                        f.close()
        finally:
            store.Quit()


class StoreRestoreCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "store-restore", aliases=["sr"],
                         summary="Restore a backup of a user store",
                         usage="%prog %cmd <store> [<backup>]")

    def RestoreStore(self, store, backupfile):
        # restore the contents of a backup file to the store we're set to
        while True:
            # read the tar header, check it's sane
            header = backupfile.read(512)
            if len(header) != 512:
                return

            # look at the file metadata, check it's sane
            document = tarfile.TarInfo.frombuf(header)
            if document.name == None:
                return # done reading from archive?

            if document.size == 0:
                if document.type == tarfile.DIRTYPE:
                    # this is a collection
                    self.RestoreCollection(store, document.name)
                continue

            # pull out file contents, and any padding
            content = backupfile.read(document.size)
            remainder = document.size % 512
            if remainder != 0:
                backupfile.read(512 - remainder) 

            # restore the file
            self.RestoreFile(store, document.name, content)

    def RestoreFile(self, store, name, content):
        # TODO:
        # check to see if guid is currently in use
        # if so, remove what's using the guid
        # create new document with correct guid
        # set other metadata
        dir_sep = name.rfind("/")
        if dir_sep < 3:
            return # invalid name
        collection = name[0:dir_sep]
        filename = name[dir_sep+1:]
        store.Write(collection, 0, content, Filename=filename)
        # print "Restore %s in %s" % (filename, collection)

    def RestoreCollection(self, store, name):
        # TODO:
        # in pseudocode, this should be:
        # check to see if guid is currently in use
        # if so, remove what's using the guid
        # create new collection with correct guid
        # set other metadata
        collection = name[0:-1]
        store.Create(collection)
        # print "Collection %s" % collection

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        backup_file = "%s.backup" % options.store
        if len(args) == 2:
            # user specified a backup file to restore
            backup_file = args[1]

        store = StoreClient(options.user, options.store)

        try:
            f = open(backup_file, "r")
            store.Store(options.store)
            self.RestoreStore(store, f)
        except IOError, e:
            print str(e)
        finally:
            if f:
                f.close()
            store.Quit()
