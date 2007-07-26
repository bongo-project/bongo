import os
import re
import socket
import errno
import cStringIO
import mailbox
import email, email.Generator
import sys

from time import time, mktime, strptime, ctime, sleep
from rfc822 import Message, parsedate_tz, mktime_tz

class MboxMailbox(mailbox.PortableUnixMailbox): 
    def __init__(self,file):
        mailbox.PortableUnixMailbox.__init__(self, file, email.message_from_file)
        self.boxname = file
        self.content = ""

    def add(self, msg):
        fp = cStringIO.StringIO()
        g = email.Generator.Generator(fp, mangle_from_=False)
        envelope = email.message_from_string(msg)
        g.flatten(envelope, unixfrom=True)
        self.content += "%s" % fp.getvalue()

    def write(self):
        outfile=file(self.boxname, "wb")
        outfile.write("%s\n" % self.content)
        outfile.close()

class MaildirMailbox(mailbox.Maildir):
    def __init__(self,file):
        if file[-1] == "/":
            self.boxname = file
        else:
            self.boxname = file + "/"

        while 1:
            try:
                os.makedirs(self.boxname + "cur")
                os.makedirs(self.boxname + "new")
                os.makedirs(self.boxname + "tmp")
            except OSError, err:
                if err.errno == errno.EEXIST:
                    break
        mailbox.Maildir.__init__(self, file, email.message_from_file)

    def tmp_open(self):
        hostname = socket.gethostname()
        pid = os.getpid()
        while 1:
            name = "tmp/%.6f%05d.%s" % (time(), pid, hostname)
            try:
                os.stat(name)
            except OSError, err:
                if err.errno == errno.ENOENT:
                    break

        fd = os.open(name, os.O_WRONLY|os.O_EXCL|os.O_CREAT, 0600)
        return (name, fd)


    def status(self, message):
        status = message.get("Status")
        if status == "O":
            dir = "cur"
            info = ""
        elif status == "RO":
            dir = "cur"
            info = ":2,S"
        else:
            dir = "new"
            info = ""

        return (dir, info)

    def get_delivery_time(self, message):
        dtime = None
        if message.has_key("Date"):
            dtime = mktime_tz(parsedate_tz(message["Date"]))

        return dtime

    def write_message(self, raw_msg, fd, tmp):
        try:
            os.write(fd, raw_msg)
            os.fsync(fd)
            os.close(fd)
        except OSError, err:
            os.unlink(tmp)
            raise Error("unable to fsync() or close() temp file: %s" % err)


    def finish_message(self, tmp, dir, info, dtime):
        base_name = os.path.basename(tmp)
        dst_name = os.path.join(dir, base_name + info)
        os.link(tmp, dst_name)

        if dtime is not None:
            atime = os.stat(dst_name).st_atime
            os.utime(dst_name, (atime, dtime))

        return dst_name

    def add(self, raw_msg):
        start_dir = os.getcwd()
        os.chdir(self.boxname)
        (tmp, fd) = self.tmp_open()

        try:
            fp = cStringIO.StringIO(raw_msg)
            message = Message(fp)
            (dir, info) = self.status(message)
            dtime = self.get_delivery_time(message)
            self.write_message(raw_msg, fd, tmp)
            dst_name = self.finish_message(tmp, dir, info, dtime)
        finally:
            os.unlink(tmp)
            os.chdir(start_dir)
