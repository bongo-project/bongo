import logging
import string
import sys

from gettext import gettext as _
import tarfile

from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from bongo.store.StoreClient import DocTypes, StoreClient

class DocumentListCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "document-list", aliases=["list"],
                         summary="List objects in a collection",
                         usage="%prog %cmd [<collection>]")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store, host=options.host, port=int(options.port), authPassword=options.password)
        
        collection = '/'
        if len(args) > 0:
            collection = args[0]
        
        try:
            store.Store(options.store)
            list = store.List(collection)
            for document in list:
                print "%s %s %s %s" % (document.uid, document.type, document.filename, document.bodylen)
        except IOError, e:
            print str(e)
        
        store.Quit()

class DocumentGetCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "document-get", aliases=["get"],
                         summary="Get a file from a user store",
                         usage="%prog %cmd <document> [<filename>]")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store, host=options.host, port=int(options.port), authPassword=options.password)
        
        if len(args) == 0:
            self.print_help()
            self.exit()
        
        if len(args) > 0:
            document = args[0]
        filename = document.split('/')[-1]
        if len(args) > 1:
            filename = args[1]
        try:
            store.Store(options.store)
            f = open(filename, "w")
            content = store.Read(document)
            f.write(content)
            f.close()
        except IOError, e:
            print str(e)
        
        store.Quit()

class DocumentPutCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "document-put", aliases=["put"],
                         summary="Put a file into a user store",
                         usage="%prog %cmd <document> <filename> [<type>]")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store, host=options.host, port=int(options.port), authPassword=options.password)
        
        if len(args) < 2:
            self.print_help()
            self.exit()
        
        document = args[0]
        filename = args[1]
        
        doc_type = 1
        if len(args) > 2:
            doc_type = args[2]
        
        docbits = document.split('/')
        if len(docbits) < 2:
            print "ERROR: Document must be a full path, e.g. /collection/documentname"
        doc_name = docbits.pop()
        doc_collection = '/'.join(docbits)
        if doc_collection == '':
            doc_collection = '/'
        
        try:
            store.Store(options.store)
            f = open(filename, "r")
            content = f.read()
            try:
                store.Info(document)
                # Info throws an exception if the document doesn't exist
                store.Replace(document, content)
            except:
                # doesn't exist, so must write the new document
                store.Write(doc_collection, doc_type, content, filename=doc_name)
                
            f.close()
        except IOError, e:
            print str(e)
        
        store.Quit()
