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
            store.Write(collection, DocTypes.Mail, msg.as_string(True))

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

class MailImapCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self,"mail-importimap", aliases=["mii"],
                        summary="Imports mail from an IMAP server",
                        usage="%prog %cmd")

        self.add_option("", "--imap_username", type="string", default="", help=_("IMAP server username"))
        self.add_option("", "--imap_password", type="string", default="", help=_("IMAP server password"))
        self.add_option("", "--imap_folder", type="string", default="INBOX", help=_("IMAP server folder to export from, defaults to INBOX"))
        self.add_option("", "--imap_server", type="string", default="", help=_("IMAP server hostname"))
        self.add_option("", "--imap_port", type="int", default=143, help=_("IMAP server port, defaults to 143"))
        self.add_option("-f", "--folder", type="string", default="INBOX", help=_("Folder to import into, defaults to INBOX"))

    def Run(self, options, args):
        if (options.imap_username == "" or
            options.imap_server == ""):
            self.print_help()
            self.exit()

        store = StoreClient(options.user, options.store)
        try:
            self.ImportMail(store, options.folder, options.imap_username, options.imap_password, options.imap_folder, options.imap_server, options.imap_port)
        finally:
            store.Quit()

    def ImportMail(self, store, folder, imap_username, imap_password, imap_folder, imap_server, imap_port):
        import imaplib

        collection = "/mail/" + folder
        store.Create(collection, existingOk=True)

        try:
            M=imaplib.IMAP4(imap_server, imap_port)
            M.login(imap_username, imap_password)
            M.select(imap_folder)
            
            try:
                typ, data = M.search(None, 'ALL')
                for num in data[0].split():
                    store.WriteImap(collection, DocTypes.Mail, M, num)

            finally:
                M.close()
                M.logout()
        finally:
            return
        
