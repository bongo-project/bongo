import email
import logging
import mailbox
import sys

from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from bongo.libs import bongojson, msgapi
from bongo.store.StoreClient import DocTypes, StoreClient

class MailStoreCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "mail-store", aliases=["ms"],
                         summary="Store individual mail messages",
                         usage="%prog %cmd <file1> [<file2> ...]")

    def ImportFile(self, store, file):
        if file == "-":
            import tempfile
            fd = tempfile.TemporaryFile(mode="w+b")
            try:
                for line in sys.stdin: fd.write(line)
            except StopIteration: pass
            fd.seek(0)
        else:
            fd = open(file)

        mbox = mailbox.PortableUnixMailbox(fd, email.message_from_file)

        for msg in mbox:
            data = msg.as_string(True)
            store.Write("/mail/INBOX", DocTypes.Mail, data)

        fd.close()

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for file in args:
                try:
                    self.ImportFile(store, file)
                except IOError, e:
                    print str(e)
        finally:
            store.Quit()
