###
# Configure slapd for embedded use
#

import base64
import logging
import os
import pwd
import random
import sha
import signal
import socket
import string
import sys
import tempfile
import time

from bongo import Xpl, Privs
from bongo.Console import wrap

log = logging.getLogger("managed-slapd")

class NullStream:
    """A substitute for stdout and stderr that writes to nowhere"""
    def write(self, s):
        pass

class ConfigSlapd:
    def __init__(self, schemaFile, slapdPath, slapdPort, adminRootpw):
        self.schemaFile = schemaFile
        self.binary = slapdPath
        self.port = slapdPort

        self.sysschemadir = Xpl.DEFAULT_SLAPD_SCHEMA_DIR

        self.suffix = "dc=example,dc=com"
        self.rootdn = "cn=admin," + self.suffix
        self.root = "\\com\\example\\admin"
        self.password = adminRootpw
        self.slapdPid = None
        self.slapdPidFile = Xpl.DEFAULT_WORK_DIR + "/bongo-slapd.pid"

        if not os.path.exists(Xpl.DEFAULT_WORK_DIR):
            os.makedirs(Xpl.DEFAULT_WORK_DIR)
            Privs.Chown(Xpl.DEFAULT_WORK_DIR)

    def slapdRunning(self, port):
        s = socket.socket()
        if s.connect_ex(("127.0.0.1", port)) != 0:
            return False
        s.close()

        if not os.path.exists(self.slapdPidFile):
            print wrap("Slapd is running, but its pid file doesn't exist.  You may need to kill slapd manually.")
            return None

        return True

    def readPidFile(self, pidFile):
        f = open(pidFile, 'r')
        line = f.readline()
        f.close()

        return int(line)

    def preDaemonize(self):
        os.chdir("/")
        os.setsid()
        os.umask(0)

        sys.stdin.close()
        sys.stdout = NullStream()
        sys.stderr = NullStream()

        for fd in range(3, 256):
            try:
                os.close(fd)
            except:
                pass

    def encodePassword(self, password):
        salt = ""
        for i in range(1, 10):
            salt = salt + random.choice(string.ascii_letters + string.digits)

        ctx = sha.new(password)
        ctx.update(salt)

        enc = base64.encodestring(ctx.digest() + salt)
        return "{SSHA}" + enc.strip()

    def startSlapd(self, confFile):
        if not os.path.exists(self.binary):
            print "Slapd path doesn't exist: %s" % self.binary
            return None

        tmp = self.slapdRunning(self.port)
        if tmp is None:
            return None

        if tmp:
            print "Found an existing slapd on port %d, using that" % self.port
            return 0

        print "Starting temporary slapd process"

        pid = os.fork()

        if not pid:
            args = [self.binary,
                    "-f", confFile,
                    "-h", "ldap://127.0.0.1:%d" % self.port,
                    "-n", "bongo-slapd"]
            if Xpl.BONGO_USER is not None:
                args.extend(("-u", Xpl.BONGO_USER))

            self.preDaemonize()

            log.debug("starting managed slapd: %s", " ".join(args))

            os.execv(self.binary, args)
            sys.exit(1)

        pid, status = os.waitpid(pid, 0)

        if (status >> 8) != 0:
            return None

        while True:
            running = self.slapdRunning(self.port)
            if running is None:
                # slapd started, but is unusable
                return None
            if running is True:
                break
            time.sleep(1)

        pid = self.slapdPid = self.readPidFile(self.slapdPidFile)
        return pid

    def killSlapd(self):
        if self.slapdPid:
            print "Shutting down temporary slapd process"
            os.kill(self.slapdPid, signal.SIGTERM)

            # wait until it has exited before continuing
            while True:
                try:
                    os.kill(self.slapdPid, 0)
                    time.sleep(1)
                except OSError:
                    break

            self.slapdPid = None

    def initSlapd(self):
        fd, tmpname = tempfile.mkstemp(".ldif")
        os.write(fd, """\
dn: %(suffix)s
objectclass: dcObject
objectclass: organization
o: Example, Inc.
dc: example

dn: %(rootdn)s
objectclass: inetOrgPerson
surname: Admin
cn: admin
userpassword: %(userpw)s
""" % {"suffix" : self.suffix, "rootdn" : self.rootdn,
       "userpw" : self.encodePassword("bongo")})

        os.close(fd)

        os.system("ldapadd -h 127.0.0.1 -p %d -x -D %s -w %s -f %s" %
                  (self.port, self.rootdn, self.password, tmpname))
        os.unlink(tmpname)

    def writeSlapdConf(self, file, writeRootpw=True):
        ldapdir = "%s/ldap" % Xpl.DEFAULT_STATE_DIR

        if not os.path.exists(ldapdir):
            os.makedirs(ldapdir)

        if Xpl.BONGO_USER is not None:
            pw = pwd.getpwnam(Xpl.BONGO_USER)
            os.chown(ldapdir, pw[2], pw[3])

        config = """\
include %(sysschemadir)s/core.schema
include %(sysschemadir)s/cosine.schema
include %(sysschemadir)s/inetorgperson.schema
include %(schema)s

%(moduleload)s
database bdb
directory %(ldapdir)s
suffix %(suffix)s
rootdn %(rootdn)s
%(rootpw)s

pidfile %(pidfile)s

index objectClass eq
lastmod on

access to attrs=userPassword
       by dn="%(rootdn)s" write
       by anonymous auth
       by self write
       by * none
access to dn.base="" by * read
access to *
       by dn="%(rootdn)s" write
       by dn="ou=Bongo Messaging Server,o=Bongo Services,%(suffix)s" write
       by * read
"""
        opts = {"schema" : self.schemaFile,
                "sysschemadir" : self.sysschemadir,
                "ldapdir" : ldapdir,
                "pidfile" : self.slapdPidFile,
                "suffix" : self.suffix,
                "rootdn" : self.rootdn,
                "moduleload" : "",
                "rootpw" : ""}

        if writeRootpw:
            opts["rootpw"] = "rootpw " + self.encodePassword(self.password)

        # quick fix for ubuntu
        if os.path.exists("/usr/lib/ldap/back_bdb.so"):
            opts["moduleload"] = "moduleload /usr/lib/ldap/back_bdb.so"

        fd = open(file, "w")
        fd.write(config % opts)
        fd.close()
