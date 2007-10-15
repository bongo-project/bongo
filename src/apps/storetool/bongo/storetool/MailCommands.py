import email
import logging
import mailbox
import sys

from gettext import gettext as _

from bongo.cmdparse import Command
from bongo.BongoError import BongoError
from libbongo.libs import bongojson, msgapi
from bongo.store.StoreClient import DocTypes, StoreClient
from bongo.storetool.ExportMailbox import MboxMailbox, MaildirMailbox

class MailImportCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "mail-import", aliases=["mi"],
                         summary="Import mail into the store",
                         usage="%prog %cmd <file1> [<file2> ...]")

        self.add_option("-t", "--type", type="string", default="mbox", help=_("Type of mail source [mbox or maildir], defaults to mbox"))
        self.add_option("-f", "--folder", type="string", default="INBOX", help=_("Folder to import to, defaults to INBOX"))

    def ImportFile(self, store, file, type, folder):
        collection = "/mail/" + folder
        store.Create(collection, existingOk=True)

        if type == "mbox":
            if file == "-":
                import tempfile
                fd = tempfile.TemporaryFile(mode="w+b")
                try:
                    for line in sys.stdin: fd.write(line)
                except StopIteration: pass
                fd.seek(0)
            else:
                fd = open(file)

            mailstore = mailbox.PortableUnixMailbox(fd, email.message_from_file)
        elif type == "maildir":
            mailstore = mailbox.Maildir(file, email.message_from_file)

        for msg in mailstore:
            data = msg.as_string(True)
            store.Write(collection, DocTypes.Mail, data)

        if fd:
            fd.close()

    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for file in args:
                try:
                    self.ImportFile(store, file, options.type, options.folder)
                except IOError, e:
                    print str(e)
        finally:
            store.Quit()


class MailExportCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "mail-export", aliases=["me"],
                         summary="Export mail from the store",
                         usage="%prog %cmd <file1> [<file2> ...]")

        self.add_option("-t", "--type", type="string", default="mbox", help=_("Type of mail destination [mbox or maildir], defaults to mbox"))
        self.add_option("-f", "--folder", type="string", default="INBOX", help=_("Folder to export from, defaults to INBOX"))

    def ExportFile(self, store, file, type, folder):
        collection = "/mail/" + folder
        mail_list = list(store.List(collection, ["nmap.guid"]))

        if type == "mbox":
            mbox = MboxMailbox(file)
            for mail in mail_list:
                if mail.type != DocTypes.Mail:
                    continue
                msg = store.Read(mail.props["nmap.guid"])
                mbox.add(msg)
            mbox.write()
        elif type == "maildir":
            maildir = MaildirMailbox(file)
            for mail in mail_list:
                if mail.type != DocTypes.Mail:
                    continue
                msg = store.Read(mail.props["nmap.guid"])
                maildir.add(msg)


    def Run(self, options, args):
        if len(args) == 0:
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)

        try:
            for file in args:
                try:
                    self.ExportFile(store, file, options.type, options.folder)
                except IOError, e:
                    print str(e)
        finally:
            store.Quit()
