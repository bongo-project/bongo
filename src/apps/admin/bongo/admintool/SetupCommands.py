import os
import logging
import shutil
import socket
import tempfile

from string import Template

from bongo.cmdparse import Command

from bongo import Privs, Xpl
from bongo.admin import Schema, Util
from bongo.bootstrap import msgapi
from bongo.Console import wrap
from bongo.MDB import MDB

import MdbUtil

class ImportCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "import",
                         summary="Import a bongo-setup.xml tree")

        self.add_option("-f", "--file", type="string")

    def run(self, options, args):
        if options.file is None:
            self.error("--file must be specified")

        if not os.path.exists(options.file):
            self.error("Specified file does not exist: %s" % options.file)

        mdb = MdbUtil.GetSetupMdb(options)

        Util.SetupFromXML(mdb, mdb.baseDn, options.file)

class SetupMdbCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "setup-mdb",
                         summary="Configure an mdb tree")

    def run(self, options, args):
        # we must use a None, None MDB here, since the admin user
        # hasn't been created yet
        mdb = MdbUtil.GetSetupMdb(options)

        Schema.GenerateMdb(Util.SCHEMA_XML, mdb)

class SetupGenSchemaCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "setup-genschema",
                         summary="Generate directory schema")

        choices = ["ad", "edir", "openldap"]
        self.add_option("-t", "--target", choices=choices)
        self.add_option("-o", "--output-file", type="string")

    def run(self, options, args):
        target = options.target

        dirs = {"ad" : Schema.GenerateAd,
                "edir" : Schema.GenerateEdir,
                "openldap" : Schema.GenerateSlapd}

        if dirs.has_key(target):
            self.log.info("generating schema")

            if options.output_file is not None:
                dirs[target](Util.SCHEMA_XML, options.output_file)
            else:
                dirs[target](Util.SCHEMA_XML)

class SetupRightsCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "setup-rights",
                         summary="Grant access to Bongo services on the Bongo tree")
        self.add_option("", "--base", type="string")

    def run(self, options, args):
        mdb = MdbUtil.GetSetupMdb(options)

        rights = {}

        for right in "admin delete read rename write".split():
            rights[right] = True

        base = options.base
        if base is None:
            base = mdb.baseDn.lstrip("\\")

        self.log.debug("granting rights on %s to Bongo services", base)
        mdb.GrantObject(base, msgapi.GetConfigProperty(msgapi.BONGO_SERVICES),
                        rights)
        #Note: nonapplicable attribute rights will get ignored
        mdb.GrantAttribute(base, "*",
                           msgapi.GetConfigProperty(msgapi.BONGO_SERVICES),
                           rights)

class SetupCredentialCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "setup-system-credential",
                         summary="Generate Bongo system agent credential")

    def run(self, options, args):
        mdb = MdbUtil.GetSetupMdb(options)

        passwd = Util.GeneratePassword(32)

        omask = os.umask(0022)

        eclients = os.path.join(Xpl.DEFAULT_DBF_DIR, "eclients.dat")
        fd = open(eclients, "w")
        fd.write(passwd)
        fd.write("\0")
        fd.close()

        os.umask(omask)

        mdb.SetPassword(msgapi.GetConfigProperty(msgapi.MESSAGING_SERVER),
                        passwd)
        passwd = Util.GeneratePassword(4096)
        mdb.SetAttribute(msgapi.GetConfigProperty(msgapi.BONGO_SERVICES),
                         msgapi.A_ACL, [passwd])

class SetupSslCommand(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "setup-ssl",
                         summary="Generate or install an SSL key/cert pair")

        self.add_option("-c", "--cert")
        self.add_option("-k", "--key")
        self.add_option("-i", "--ip")
        self.add_option("-d", "--domain")

    def run(self, options, args):
        bc = Xpl.DEFAULT_BIN_DIR + "/bongo-config"

        ret = os.system("%s --ip %s --domain %s crypto" % (bc, options.ip, options.domain))

        if options.cert and options.key:
            self.log.info("Copying the requested certificate and key into place")

            if not os.path.exists(options.cert):
                self.error("Certificate file does not exist")
            if not os.path.exists(options.key):
                self.error("Key file does not exist")

            shutil.copyfile(options.cert, certpath)
            shutil.copyfile(options.key, keypath)
