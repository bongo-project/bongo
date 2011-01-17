import logging
import string
import sys

from gettext import gettext as _
import tarfile

from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from bongo.store.StoreClient import DocTypes, StoreClient

# import/export store contents as tar files
# ideally we want to farm the tar format stuff itself out into a separate lib.
# Sadly, the TarFile implementation that comes with Python is mostly useless 

class PAXHeader:
    def __init__(self, filename):
        self.keywords = {}
        self.filename = filename

    def SetKey(self, key, value):
        self.keywords[key] = value

    def Keys(self):
        return self.keywords

    def ToString(self):
        header = tarfile.TarInfo(self.filename + ".metadata")
        header.type = "x"

        content = ""
        for key in self.keywords.keys():
            if self.keywords[key] != None:
                content += "%d %s=%s\n" % (len(key) + len(self.keywords[key]) + 5, key, self.keywords[key])
        header.size = len(content)

        result = header.tobuf()
        result += content
        remainder = len(content) % 512
        if remainder != 0:
            result += "\0" * (512 - remainder)

        return result

    def FromString(self, content):
        for line in string.split(content, "\n"):
            space = string.find(line, " ")
            equals = string.find(line, "=")
            key = line[space+1:equals]
            value = line[equals+1:]
            self.keywords[key] = value

class StoreBackupCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")
    user = "admin"
    store = "_system"

    def __init__(self):
        Command.__init__(self, "store-backup", aliases=["sb"],
                         summary="Create a backup of a store",
                         usage="%prog %cmd [<output>]")

    def GetStoreConnection(self):
        return StoreClient(self.user, self.store, host=self.host, port=int(self.port), authPassword=self.password)

    def BackupStore(self):
        # create a backup file which contains all the contents of the given store
        store = self.GetStoreConnection()
        docstore = self.GetStoreConnection()

        try:
            backup_file = open("%s.backup" % self.store, "wb")
        except IOError, e:
            print str(e)
            return

        collection_list = ['/']
        for collection in store.Collections():
            # write out entries for collections
            tar_head = PAXHeader(collection.name)
            tar_head.SetKey("BONGO.uid", collection.uid)
            tar_head.SetKey("BONGO.type", "%d" % collection.type)
            tar_head.SetKey("BONGO.flags", "%d" % collection.flags)
            props = docstore.PropGet(collection.uid)
            for key in props.keys():
                tar_head.SetKey("BONGO.%s" % key, props[key])
            backup_file.write(tar_head.ToString())

            tar_dir = tarfile.TarInfo(collection.name)
            tar_dir.size = 0
            tar_dir.type = tarfile.DIRTYPE
            tar_dir.mode = 0755
            backup_file.write(tar_dir.tobuf())
            collection_list.append(collection.name)

        for collection in collection_list:
            for document in store.List(collection):
                # write out the documents in the collection
                if document.filename[0] == '/' or document.type & DocTypes.Folder:
                    # this is a collection
                    continue
                if document.type == DocTypes.Conversation or document.type == DocTypes.Calendar:
                    # DB-only file type, don't attempt to back this up
                    continue
                if collection == '/conversations':
                    # this is a hack; Bongo 0.3 doesn't seem to mark all conversations
                    # as such correctly :(
                    continue
                filename = "%s/%s" % (collection, document.filename)
                try:
                    content = docstore.Read(filename)
                except IOError, e:
                    print str(e)
                    continue
               
                # write out meta data associated with document in a PAX extended header
                headerx = PAXHeader(filename)
                headerx.SetKey("BONGO.uid", document.uid)
                headerx.SetKey("BONGO.imapuid", document.imapuid)
                headerx.SetKey("BONGO.type", "%d" % document.type)
                headerx.SetKey("BONGO.flags", "%d" % document.flags)
                props = docstore.PropGet(document.uid)
                for key in props.keys():
                    value = props[key]
                    if value != None:
                        headerx.SetKey("BONGO.%s" % key, value.encode("utf-8"))
                header_content = headerx.ToString()
                
                backup_file.write(header_content)

                # tar files have a 512 byte header (tar_info.tobuf()), and then the content
                # content is padded to a multiple of 512 bytes
                tar_info = tarfile.TarInfo(filename)
                tar_info.size = len(content)
                tar_info.mode = 0644
                tar_info.mtime = int(document.created)
                backup_file.write(tar_info.tobuf())
                backup_file.write(content)
                space = len(content) % 512
                if space != 0:
                    backup_file.write("\0" * (512 - space))

        store.Quit()
        docstore.Quit()

        backup_file.write("\0" * 1024) # end of archive marker
        backup_file.close()

    def Run(self, options, args):
        if len(args) == 1 and args[0] == "help":
            self.print_help()
            self.exit()

        self.host = options.host
        self.port = options.port
        self.user = options.user
        self.password = options.password
        self.store = options.store
        self.BackupStore()

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
            try:
                extheader = tarfile.TarInfo.frombuf(header)
            except:
                # probably the end of the file
                return

            if extheader.type != "x": 
                print "No metadata header found (%s)" % extheader.name
                continue

            metadata = PAXHeader(None)
            metadata.FromString(backupfile.read(extheader.size))
            remainder = extheader.size % 512
            if remainder != 0:
                backupfile.read(512 - remainder)

            header = backupfile.read(512)
            if len(header) != 512:
                return

            document = tarfile.TarInfo.frombuf(header)
            if document.name == None:
                return # done reading from archive?

            if document.size == 0:
                if document.type == tarfile.DIRTYPE:
                    # this is a collection
                    self.RestoreCollection(store, document.name, metadata.Keys())
                continue

            # pull out file contents, and any padding
            content = backupfile.read(document.size)
            remainder = document.size % 512
            if remainder != 0:
                backupfile.read(512 - remainder) 

            # restore the file
            self.RestoreFile(store, document.name, content, metadata.Keys())

    def RestoreProps(self, store, guid, metadata):
        del metadata["BONGO.nmap.guid"]
        del metadata["BONGO.nmap.type"]
        del metadata["BONGO.nmap.flags"]
        del metadata["BONGO.nmap.collection"]
        del metadata["BONGO.nmap.index"]
        if "BONGO.uid" in metadata:
            del metadata["BONGO.uid"]
        if "BONGO.type" in metadata:
            del metadata["BONGO.type"]
        if "BONGO.flags" in metadata:
            del metadata["BONGO.flags"]
        if "BONGO.imapuid" in metadata:
            del metadata["BONGO.imapuid"]
        for key, value in metadata.items():
            if key[0:6] == "BONGO.":
                prop = key[6:]
                try:
                    store.PropSet(guid, prop, value)
                except:
                    print "Failed to set property %s to %s on %s" % (prop, value, guid)

    def RestoreFile(self, store, name, content, metadata):
        dir_sep = name.rfind("/")
        if dir_sep < 3:
            return # invalid name
        collection = name[0:dir_sep]
        filename = name[dir_sep+1:]
        file_guid = None

        if "BONGO.uid" in metadata:
            file_guid = metadata["BONGO.uid"]
            del metadata["BONGO.uid"]
            
        file_type = 0
        if "BONGO.type" in metadata:
            file_type = int(metadata["BONGO.type"])
            del metadata["BONGO.type"]

        try:
            new_guid = store.Write(collection, file_type, content, filename=filename, guid=file_guid, noProcess=True)
        except: 
            print "Failed to restore %s/%s" % (collection, filename)
            return
        if "BONGO.flags" in metadata:
            try:
                store.Flag(new_guid, flags=int(metadata["BONGO.flags"]))
                del metadata["BONGO.flags"]
            except:
                print "Couldn't restore mail flag on %s/%s" % (collection, filename)
        self.RestoreProps(store, new_guid, metadata)

    def RestoreCollection(self, store, name, metadata):
        collection = name[0:-1]
        try:
            if "BONGO.uid" in metadata:
                store.Create(collection, guid=metadata["BONGO.uid"])
                del metadata["BONGO.uid"]
            else:
                store.Create(collection)
        except:
            print "Failed to create collection %s" % collection
        self.RestoreProps(store, collection, metadata)

    def ClearStore(self, store):
        collections = [collection.name for collection in store.Collections()]
        collections.sort(lambda x, y: len(x)-len(y), reverse=True)
        for collection in collections:
             store.Remove(collection)

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        backup_file = "%s.backup" % options.store
        if len(args) == 1:
            # user specified a backup file to restore
            backup_file = args[0]

        store = StoreClient(options.user, options.store)

        f = None
        try:
            f = open(backup_file, "r")
            store.Store(options.store)
            self.ClearStore(store)
            self.RestoreStore(store, f)
        except IOError, e:
            print str(e)
        if f:
            f.close()
        store.Quit()
