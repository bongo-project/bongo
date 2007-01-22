import bongo.external.simplejson as simplejson
import bongo.external.vobject as vobject
import bongo.table as table

import logging

from bongo.cmdparse import Command
from bongo.Contact import Contact
from bongo.store.StoreClient import DocTypes, StoreClient

class AddressbooksCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "addressbook-list", aliases=["al"],
                         summary="List the addressbooks in your store")

    def Run(self, options, args):
        store = StoreClient(options.user, options.store)

        cols = ["Name"]
        rows = []

        try:
            abs = list(store.List("/addressbook"))
            for ab in abs:
                rows.append((ab.filename,))
        finally:
            store.Quit()

        print table.format_table(cols, rows)

class AddressbookContactsCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "addressbook-contacts", aliases=["ac"],
                         summary="List the contacts in an addressbook",
                         usage="%prog %cmd <addressbook>")

    def _FindAddressbook(self, store, name):
        abs = list(store.List("/addressbook"))
        for ab in abs:
            if ab.filename == name:
                return ab

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        cols = ["Name"]
        rows = []

        try:
            ab = self._FindAddressbook(store, args[0])
            if ab is None:
                print "Could not find addressbook named '%s'" % args[0]
                return

            contacts = list(store.List(ab.uid, ["nmap.document"]))

            for contact in contacts:
                jsob = simplejson.loads(contact.props["nmap.document"].strip())

                name = jsob.get("fn")

                rows.append((name,))
        finally:
            store.Quit()

        rows.sort()
        print table.format_table(cols, rows)

class ImportVcfCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "addressbook-import", aliases=["ai"],
                         summary="Import VCF data from files",
                         usage="%prog %cmd <file1> [<file2> ...]")

    def Run(self, options, args):
        if len(args) < 1:
            self.print_usage()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for file in args:
                self.ImportVcfFile(store, file)
        finally:
            store.Quit()

    def ImportVcfFile(self, store, file):
        fd = open(file, 'r')

        for v in vobject.readComponents(fd):
            c = Contact(v)
            store.Write("/addressbook/personal", DocTypes.Addressbook,
                        c.AsJson())

        fd.close()
