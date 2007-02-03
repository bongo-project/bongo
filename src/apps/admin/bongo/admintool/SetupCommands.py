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
        ct = os.popen('which certtool').read().strip()
        if ct is '':
            self.error("Cannot find GNUTLS certtool")
            return

        certpath = Xpl.DEFAULT_CERT_PATH
        keypath = Xpl.DEFAULT_KEY_PATH
        rsapath = Xpl.DEFAULT_RSAPARAMS_PATH
        dhpath = Xpl.DEFAULT_DHPARAMS_PATH
        rngpath = Xpl.DEFAULT_RANDSEED_PATH

        cmds = []
        t, template = tempfile.mkstemp()

        if options.cert and options.key:
            self.log.info("Copying the requested certificate and key into place")

            if not os.path.exists(options.cert):
                self.error("Certificate file does not exist")
            if not os.path.exists(options.key):
                self.error("Key file does not exist")

            shutil.copyfile(options.cert, certpath)
            shutil.copyfile(options.key, keypath)
        else:
            self.log.info("Generating a new cert/key pair")
            cmds.append([keypath, '--generate-privkey --bits 1024'])
            cmds.append([certpath, ' -s --load-privkey ' + keypath + ' --template ' + template])
            format = Template("organization = \"Bongo Project\"" \
                    "unit = \"Fake Certs Dept.\"\nstate = \"None\"\ncountry = US \n" \
                    "cn = ${cn} \nserial = 0001 \nexpiration_days = 700 \n" \
                    "dns_name = ${dns} \nip_address = ${ip} \nsigning_key")
            os.write(t, format.substitute(cn = options.domain, dns = options.domain, ip = options.ip))

        os.close(t)
	cmds.append([dhpath, '--generate-dh-params --bits 1024'])
        cmds.append([rsapath, '--generate-privkey --bits 512'])

        self.log.info("Creating encryption data. *This may take some time!*")
        for cmdpair in cmds:
            path, cmdopts = cmdpair
            command = ct + ' ' + cmdopts + ' --outfile ' + path + ' > /dev/null'
            if not os.path.exists(os.path.dirname(path)):
                os.makedirs(path)
            self.log.info("Creating %s...", path)
            self.log.debug("running: %s", command)
            ret = os.system(command) / 256
            if ret:
                self.error("Error!")

        os.remove(template)
