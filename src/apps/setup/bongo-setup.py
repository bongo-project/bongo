#!/usr/bin/env python
#############################################################################
#
#  Copyright (c) 2006 Novell, Inc.
#  All Rights Reserved.
#
#  This program is free software; you can redistribute it and/or
#  modify it under the terms of version 2.1 of the GNU Lesser General Public
#  License as published by the Free Software Foundation.
#
#  This program is distributed in the hope that it will be useful,
#  but WITHOUT ANY WARRANTY; without even the implied warranty of
#  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
#  GNU Lesser General Public License for more details.
#
#  You should have received a copy of the GNU Lesser General Public License
#  along with this program; if not, contact Novell, Inc.
#
#  To contact Novell about this file by physical or electronic mail,
#  you may find current contact information at www.novell.com
#
#############################################################################

import os, random, signal, socket, string, sys

from bongo import Xpl, BongoError, Privs
from bongo.admin import ManagedSlapd, Util
from bongo.bootstrap import msgapi

from bongo.Console import wrap

import re, optparse

import logging
formatter = logging.Formatter("%(levelname)s: %(message)s")
console = logging.StreamHandler()
console.setFormatter(formatter)
logging.root.addHandler(console)

log = logging.getLogger("bongo-setup")

# the managed slapd configuration is global, so we can shut it down on
# failure
slapdConfig = None

def BongoSetupOptions():
    parser = optparse.OptionParser()
    parser.add_option("-n", "--noprompt", action="store_true", help="run non-interactively")
    parser.add_option("-H", "--hostname", type="string", help="hostname of this Bongo server")
    parser.add_option("-M", "--maildomain", type="string", help="domain we accept mail for")
    parser.add_option("-f", "--file", type="string",
                      default=os.path.join(Xpl.DEFAULT_CONF_DIR, "bongo-setup.xml"),
                      help="import objects from XML file FILE")

    group = optparse.OptionGroup(parser, "SSL options")
    group.add_option("-r", "--cert", type="string", metavar="FILE", help="use certificate file FILE")
    group.add_option("-k", "--key", type="string", metavar="FILE", help="use private key file FILE")
    parser.add_option_group(group)

    group = optparse.OptionGroup(parser, "LDAP options", description="options for --mdb-driver=ldap")
    group.add_option("-u", "--authuser", type="string", help="user for directory authentication")
    group.add_option("-p", "--authpass", type="string", help="password for directory authentication")
    parser.add_option_group(group)

    schemas = "ad edir mdb openldap".split()
    parser.add_option("-S", "--gen-schema", choices=schemas, metavar="TARGET", help="generate schema for TARGET (" + ", ".join(schemas) + ")")
    parser.add_option("-a", "--noacl", type="string", help="don't set up ACLs")

    dirs = "file ldap managed-slapd".split()
    parser.add_option("-m", "--mdb-driver", choices=dirs, default="managed-slapd", help="specify directory driver (" + ", ".join(dirs) + ") [default %default]")
    parser.add_option("-b", "--base", type="string", default="\\Tree", help="base in directory for Bongo")

    parser.add_option("", "--managed-slapd-path", type="string", metavar="PATH",
                      default=Xpl.DEFAULT_SLAPD_PATH,
                      help="path to your slapd binary [default %default]")
    parser.add_option("", "--managed-slapd-port", type="int", metavar="PORT",
                      default=8983, help="port for managed slapd [default %default]")

    admintool = os.path.join(Xpl.DEFAULT_BIN_DIR, "bongo-admin")

    binpath = os.path.abspath(os.path.dirname(sys.argv[0]))

    if not binpath.startswith(Xpl.DEFAULT_BIN_DIR):
        adminpath = os.path.abspath(os.path.join(binpath, "..", "bongoadmin",
                                                 "bongo-admin"))
        if os.path.exists(adminpath):
            admintool = adminpath

    parser.add_option("", "--bongo-admin", action="store_const", default=admintool, help=optparse.SUPPRESS_HELP)
    parser.add_option("", "--debug", action="store_true", help=optparse.SUPPRESS_HELP)

    return parser

def main():
    parser = BongoSetupOptions()
    (options, args) = parser.parse_args()

    if options.debug:
        logging.root.setLevel(logging.DEBUG)

    log.debug("bongo-setup options: " + str(options))

    if len(args) > 0:
        log.error("Unexpected arguments after script flags")
        sys.stderr.write(parser.get_usage())
        sys.stderr.write("\n")
        sys.exit(1)

    if os.getegid() != 0:
        log.error("%s must be run as root" % os.path.basename(sys.argv[0]))
        sys.exit(1)

    if not options.noprompt:
        print wrap("""
Welcome to Bongo!

You will be guided through the setup of a new Bongo installation.

This tool makes use of %(bongo_admin)s, which you can also use to tweak the system later, or (for experts) to set up Bongo manually.

See \"%(bongo_setup)s --help\" for command-line options to further automate the setup process.
""" % {'bongo_admin' : options.bongo_admin, 'bongo_setup' : sys.argv[0]})

    Input('Press Enter to continue', options=options)

    # make sure our various directories are in good shape permissions-wise
    for path in [Xpl.DEFAULT_CONF_DIR,
                 Xpl.DEFAULT_STATE_DIR,
                 Xpl.DEFAULT_CACHE_DIR,
                 Xpl.DEFAULT_DBF_DIR,
                 Xpl.DEFAULT_WORK_DIR]:
        if not os.path.exists(path):
            os.makedirs(path)
        Privs.Chown(path)

    pidfile = os.path.join(Xpl.DEFAULT_WORK_DIR, "bongomanager.pid")
    if os.path.exists(pidfile):
        fd = open(pidfile, "r")
        pid = fd.readline().strip()
        fd.close()

        # see if the bongomanager pid is still running
        try:
            pid = int(pid)
            r = os.kill(pid, 0)
            raise BongoError("""A possibly stale bongomanager pid file has been found.  Either kill process %d (if it is bongomanager) or remove %s""" % (pid, pidfile))
        except OSError, e:
            os.remove(pidfile)

    mdbfile = os.path.join(Xpl.DEFAULT_CONF_DIR, "mdb.conf")

    mdbconf = InspectMDB(mdbfile, options)
    if mdbconf is None:
        mdbconf = SetupMDB(mdbfile, options)

        if mdbconf.get("driver") in ("file", "managed-slapd"):
            if not UpdateSchema(mdbconf, options):
                raise BongoError(wrap("""Updating the MDB schema has failed."""))

    if ImportObjects(mdbconf, options):
        raise BongoError("""Importing objects failed, possibly because the directory's schema needs to be updated. When you are done updating the schema in your directory, run %s again to complete Bongo's configuration.""" % sys.argv[0])

    print 'Imported objects from \"%s\".' % options.file
    SetupACL(mdbconf, options)
    GeneratePasswords(options)

    print 'Configuring final settings...'
    from bongo import MDB
    mdb = MDB.MDB(options.authuser, options.authpass)
    context = mdb.baseDn

    # include other directory options here once they're supported
    if mdbconf.get("driver") == "ldap" and not mdbconf.has_key("slapdConfig"):
        context = Input('Enter default directory context for Bongo users',
                        {}, mdb.baseDn, options)

        while not mdb.IsObject(context):
            print 'Context does not exist. Please enter a valid context.'
            context = Input('Enter default directory context for Bongo users',
                            {}, mdb.baseDn, options)

    confprops = {msgapi.DEFAULT_CONTEXT : context}
    Util.ModifyConfProperties(confprops)

    msgserver = msgapi.GetConfigProperty(msgapi.MESSAGING_SERVER)
    mdb.SetAttribute(msgserver, msgapi.A_CONTEXT, [context])

    #getting hostname for certificate
    if options.hostname:
        hostname = options.hostname
    else:
        hostname = Input('Enter the hostname for the mail server hosting Bongo',
                       {}, socket.getfqdn(), options)

    try:
        mdb.SetAttribute(msgserver, msgapi.A_OFFICIAL_NAME, [hostname])
    except BongoError:
        if not hostname in ['localhost', 'localhost.localdomain']:
            raise

    #getting domain name bongo will accept mail for
    if options.maildomain:
        maildomain = options.maildomain
    else:
        maildomain = Input('Enter domain name that Bongo will host',
                       {}, socket.getfqdn(), options)

    try:
        mdb.SetAttribute(msgserver, msgapi.A_OFFICIAL_NAME, [maildomain])
    except BongoError:
        if not maildomain in ['localhost', 'localhost.localdomain']:
            raise

    # Add hosted domain to the smtp agent
    smtpserver = msgserver + '\\' + msgapi.AGENT_SMTP
    if(mdb.IsObject(smtpserver)):
        try:
            mdb.AddAttribute(smtpserver, msgapi.A_DOMAIN, maildomain)
        except:
            print "Couldn't set attribute A_DOMAIN to " + maildomain
    try:
        default = socket.gethostbyname(maildomain)
    except:
        default = '127.0.0.1'

    ip = Input('Enter IP address on which to run Bongo',
               {}, default, options)
    try:
        mdb.SetAttribute(msgserver, msgapi.A_IP_ADDRESS, [ip])
    except:
        print "Couldn't set attribute A_IP_ADDRESS to " + ip


    options.hostname = hostname
    options.ip = ip

    # now we have the hostname and ip, we can setup a sensible cert.
    SetupCerts(options)

    # shut down the temporary slapd
    if mdbconf.has_key("slapdConfig"):
        slapdConfig = mdbconf["slapdConfig"]
        slapdConfig.killSlapd()

    print
    print 'Bongo setup is complete!'
    print 'Now run "bongo-config install" to complete the installation' 
    return

def ReadMdbConf(mdbfile):
    fd = open(mdbfile)
    confstr = fd.readline().strip()
    fd.close()

    if not re.match("^Driver=", confstr):
        return None

    arr = confstr.split(" ", 2)
    driver = arr[0].split("=", 2)

    conf = {"driver" : driver[1].lower()}
    if len(arr) > 1:
        for key, val in re.findall("(.*?)=\"(.*?)\"(?:[ |,])?", arr[1]):
            conf[key.lower()] = val

    return conf

def WriteMdbConf(mdbfile, mdbconf):
    mcfile = open(mdbfile, "w")

    drivers = {"managed-slapd" : "ldap"}

    driver = mdbconf.get("driver")
    if drivers.has_key(driver):
        driver = drivers[driver]

    confstr = "Driver=MDB" + driver.upper()

    opts = []
    for optname in ["basedn", "binddn", "password", "port", "host", "tls"]:
        if not mdbconf.has_key(optname):
            continue

        opts.append('%s="%s"' % (optname, mdbconf[optname]))

    if len(opts) > 0:
        confstr = confstr + " " + ",".join(opts)

    mcfile.write(confstr)
    mcfile.write("\n")
    mcfile.close()
    Privs.Chown(mdbfile)

def LdapToMdb(dn):
    parts = re.findall("=([^,]+)", dn)
    parts.reverse()

    return '\\' + '\\'.join(parts)

def InspectMDB(mdbfile, options):
    if not os.path.exists(mdbfile):
        return None

    log.debug("inspecting existing MDB install")

    conf = ReadMdbConf(mdbfile)

    if conf is None:
        return None

    olddriver = conf["driver"]

    print 'Found an existing %s MDB configuration.' % olddriver
    choices = {'keep': 'Use the existing \"%s\" configuration' % olddriver,
               'change': 'Set up MDB differently'}
    choice = 'keep'
    if options.noprompt:
        choice = 'change'

    choice = Input('Enter your choice for MDB', choices, choice, options)

    if choice == 'change':
        return None

    # at this point, the user wants to reuse the old mdb configuration
    drivers = {'mdbfile': 'file', 'mdbldap': 'ldap'}

    # if no driver has been specified on the command line, reuse
    # the old driver
    if options.mdb_driver is None:
        conf["driver"] = drivers.get(conf["olddriver"], conf["olddriver"])

    if options.authuser and options.authpass:
        print 'Using command line credentials...'
    elif olddriver == 'mdbfile':
        print 'Using no credentials (for mdbfile)...'
    else:
        authuser = conf.get('binddn')
        authpass = conf.get('password')

        if not authuser and not authpass:
            print 'Could not determine directory credentials from old MDB configuration.'
            authuser = Input('Enter the directory admin dn')
            authpass = Input('Enter the directory admin password')

        if authuser and not options.authuser:
            # turn the ldap user into an mdb user
            options.authuser = LdapToMdb(authuser)

        if authpass and not options.authpass:
            options.authpass = authpass

    return conf

def GetRandomPassword():
    pw = ""
    chars = string.ascii_letters + string.digits
    for i in range(1, 20):
        pw = pw + random.choice(chars)
    return pw

def DiscoverMDBDriverCB(drivers, parent, children):
    for child in children:
        if os.path.isdir(os.path.join(parent, child)):
            children.remove(child)
        if (child[:6].lower() == 'libmdb') and (child[-3:].lower() == '.so'):
            drivers[child[6:-3]] = child.lower()

def DiscoverMDBDrivers():
    mdbpathlib = "%s/bongomdb/" % Xpl.DEFAULT_LIB_PATH
    if not os.path.exists(mdblibpath):
        raise BongoError(wrap("""MDB Driver directory can not be found!"""))
    drivers = {}
    os.path.walk(mdblibpath, DiscoverMDBDriverCB, drivers)
    return drivers

def SetupMDB(mdbfile, options):
    drivers = DiscoverMDBDrivers()
    mdbOptions = {}
    if drivers.has_key('file'):
        mdbOptions['file'] = 'use the filesystem as a directory (test systems only)'
    if drivers.has_key('ldap'):
        mdbOptions['managed-slapd'] = 'Bongo-managed standalone LDAP server'
        mdbOptions['ldap'] = 'connect to any LDAP-speaking directory'
    if drivers.has_key('edir'):
        mdbOptions['edir'] = 'use native calls to speak to eDirectory'
    if len(mdbOptions) == 0:
        raise BongoError(wrap("""No MDB Driver has been found!"""))
    print 'Which driver would you like to use for the directory?'
    mdbSelection = Input('Enter your driver selection', mdbOptions, options.mdb_driver, options)

    mdbconf = {}
    mdbconf["driver"] = mdbSelection

    confprops = {"ManagedSlapd" : 0}

    if mdbSelection == 'file':
        mdbpath = os.path.join(Xpl.DEFAULT_STATE_DIR, "mdb")
        if not os.path.exists(mdbpath):
            os.makedirs(mdbpath)
        Privs.Chown(mdbpath)
        options.authpass = "bongo"
    elif mdbSelection == 'ldap':
        host = ShellCmd('hostname --long')
        if host:
            default = 'dc=' + ',dc='.join(host.split('.'))
        else:
            default = 'dc=example,dc=com'

        ldaphost = Input('Enter the hostname or the IP address of the ldap server', default='localhost',
                       options=options)
        mdbconf["host"] = ldaphost
        ldapport = Input('Enter the port of the ldap server', default='389',
                       options=options)
        mdbconf["port"] = ldapport

        basedn = Input('Enter LDAP base for Bongo', default=default,
                       options=options)
        mdbconf["basedn"] = basedn

        binddn = Input('Enter LDAP admin object', default='cn=admin,'+basedn,
                       options=options)
        mdbconf["binddn"] = options.authuser = binddn

        bindpw = Input('Enter LDAP admin password', default='bongo',
                       options=options)
        mdbconf["password"] = options.authpass = bindpw
        enableTLS = Input('Use TLS on LDAP connections', default='true',
                       options=options)
        if enableTLS[0:1].lower() == 't':
            mdbconf["tls"] = '1'
    elif mdbSelection == 'managed-slapd':
        ofile = os.path.join(Xpl.DEFAULT_CONF_DIR, "bongo.schema")

        bongoRootpw = GetRandomPassword()

        global slapdConfig
        slapdConfig = \
                    ManagedSlapd.ConfigSlapd(ofile,
                                             options.managed_slapd_path,
                                             options.managed_slapd_port,
                                             bongoRootpw)

        mdbconf["port"] = options.managed_slapd_port
        mdbconf["basedn"] = slapdConfig.suffix
        mdbconf["binddn"] = slapdConfig.rootdn
        mdbconf["password"] = slapdConfig.password

        slapdConfigFile = os.path.join(Xpl.DEFAULT_CONF_DIR, "bongo-slapd.conf")
        slapdConfig.writeSlapdConf(slapdConfigFile)

        options.authuser = LdapToMdb(slapdConfig.rootdn)
        options.authpass = slapdConfig.password

        mdbconf["slapdConfig"] = slapdConfig
        mdbconf["slapdConfigFile"] = slapdConfigFile

        base = '\\com\\example'
        confprops = {'BongoServices': '%s\\%s' % (base, msgapi.ROOT),
                     'MessagingServer': '%s\\%s\\%s' % (base,
                                                        msgapi.ROOT,
                                                        'Bongo Messaging Server'),
                     'ManagedSlapd': '1',
                     'ManagedSlapdPath': options.managed_slapd_path,
                     'ManagedSlapdPort': str(options.managed_slapd_port)}
    elif mdbSelection == 'edir':
        edirhost = Input('Enter the hostname or the IP address of the edirectory (ncp) server', default='localhost',
                       options=options)
        mdbconf["host"] = edirhost
        edirport = Input('Enter the port of the edirectory (ncp) server', default='524',
                       options=options)
        mdbconf["port"] = edirport
    else:
        raise BongoError('Invalid driver selection: %s' % mdbSelection)
    Util.WriteConfProperties(confprops)

    print 'Configured MDB with \"%s\" option.' % mdbSelection
    WriteMdbConf(mdbfile, mdbconf)
    return mdbconf
    
def ImportObjects(mdbconf, options):
    print 'Directory setup - importing objects...'

    pid = None
    if mdbconf.has_key("slapdConfig"):
        slapdConfig = mdbconf["slapdConfig"]

        log.debug("starting temporary managed slapd process")
        pid = slapdConfig.startSlapd(mdbconf["slapdConfigFile"])
        if pid is None:
            raise BongoError('Error starting temporary slapd process. Check syslog for information about the failure.')

        log.debug("initializing slapd with the base Bongo container")
        slapdConfig.initSlapd()

    options.file = Input('Enter file for importing objects',
                 default=options.file, options=options)

    authops = GetAdminCmdAuth(options)
    return AdminCmd(options, authops + ['import', '-f', options.file])

def UpdateSchema(mdbconf, options):
    print 'Updating the directory\'s schema...'

    authops = GetAdminCmdAuth(options)

    dirnames = {"file" : "MDB File",
                "edir" : "Novell eDirectory",
                "openldap" : "OpenLDAP",
                "managed-slapd" : "Managed Slapd"}

    driver = mdbconf["driver"]

    print 'Generating schema for %s...' % dirnames.get(driver)

    if driver == 'file' or driver == 'edir':
        if AdminCmd(options, authops + ['setup-mdb']):
            return False
        return True

    elif driver == 'ldap':
        dirtypes = {'openldap': 'OpenLDAP standalone server',
                    'edir': 'Novell eDirectory',
                    'ad': 'Microsoft Active Directory'}
        dirtype = Input('Enter your directory selection', dirtypes,
                        'openldap', options)
        if dirtype == 'openldap':
            ofile = 'bongo.schema'
        elif dirtype == 'edir':
            ofile = 'bongo.sch'
        elif dirtype == 'ad':
            ofile = 'bongo.ldf'
        else:
            raise BongoError('Invalid directory type')
            
        print 'A schema file will now be generated, which you will have to'
        print 'import into your directory manually.'
        ofile = Input('Enter filename for schema', {}, ofile, options)
        if AdminCmd(options, authops + ["setup-genschema",
                                        "-t", dirtype,
                                        "-o", ofile]):
            return False

        return True

    elif driver == 'managed-slapd':
        ofile = os.path.join(Xpl.DEFAULT_CONF_DIR, 'bongo.schema')
        if AdminCmd(options, authops + ["setup-genschema",
                                        "-t", "openldap",
                                        "-o", ofile]):
            return False

        return True

def GetAdminCmdAuth(options):
    authops = []
    if options.authuser:
        authops.extend(["-U", options.authuser])
    if options.authpass:
        authops.extend(["-P", options.authpass])
    return authops

def GetAdminCmdBase(options):
    if not options.base:
        return []
    return ["--base", options.base]

def SetupACL(mdbconf, options):
    noacl_drivers = ("managed-slapd")

    if options.noacl or mdbconf["driver"] in noacl_drivers:
        print 'Skipping access control setup.'
        return

    print 'Setting up access control...'

    authops = GetAdminCmdAuth(options)
    baseops = GetAdminCmdBase(options)

    ret = AdminCmd(options, authops + ['setup-rights'] + baseops)
    if ret != 0:
        print 'Rights could not be automatically set in the directory.'
        print 'You will manually have to grant privileges on the base'
        print 'to the Bongo Messaging Server object.'
        Input('Press Enter to continue')
    else:
        print 'Access control installed.'

def GeneratePasswords(options):
    print 'Generating server credentials...'

    authops = GetAdminCmdAuth(options)

    if AdminCmd(options, authops + ['setup-system-credential']) != 0:
        raise BongoError('Server credential generation failed.')

    print 'Server credentials installed.'

#TODO: ask for files interactively
def SetupCerts(options):
    authops = GetAdminCmdAuth(options)

    certops = []
    if options.cert and options.key:
        print 'Installing provided certificate + key...'
        certops.append('--cert=\"%s\"' % options.cert)
        certops.append('--key=\"%s\"' % options.key)

    if not certops:
        print 'Generating certificate + key...'
        certops.append('--ip=\"%s\"' % options.ip)
        certops.append('--domain=\"%s\"' % options.hostname)

    if AdminCmd(options, authops + ['setup-ssl'] + certops) != 0:
        raise BongoError('Certificate + key installation failed.')

    print 'Certificate + key installed.'

def AdminCmd(options, args):
    if not args:
        raise BongoError('No command specified')

    if options.debug:
        args = ["--debug"] + args

    log.debug("running %s %s", options.bongo_admin, ' '.join(args))
    ret = os.spawnv(os.P_WAIT, options.bongo_admin, [options.bongo_admin] + args)

    if ret != 0:
        log.debug("command failed")

    return ret

def ShellCmd(cmd):
    log.debug("running shell command: " + cmd)
    return os.popen(cmd).read().strip()

def Input(prompt, choices={}, default=None, options=None):
    if options and options.noprompt:
        return default

    nums = []
    length = 0
    for key, val in choices.iteritems():
        nums.append(key)
    nums.sort()
    for key in choices.keys():
        if length < len(key):
            length = len(key)

    entry = None
    while entry is None:
        i = 0
        for val in nums:
            i += 1
            print '    %d: %s - %s' % (i, val.ljust(length), choices[val])
        if default is None:
            pstr = prompt + ': '
        else:
            pstr = '%s [%s]: ' % (prompt, default)
        entry = raw_input(pstr)
        if entry.lower().strip() in choices.keys():
            entry = entry.lower().strip()
        elif entry.strip().isdigit() and int(entry.strip()) <= len(nums):
            entry = nums[int(entry.strip()) - 1]
        elif not entry.strip():
            entry = default
            if default is None:
                break
        elif entry and not choices:
            entry = entry
        else:
            entry = None

    return entry

def Shutdown():
    if slapdConfig is not None:
        slapdConfig.killSlapd()

if __name__ == "__main__":
    try:
        main()
        Shutdown()
    except BongoError, e:
        Shutdown()
        print wrap("ERROR: " + str(e))
        sys.exit(1)
    except KeyboardInterrupt:
        Shutdown()
        print
        sys.exit(1)
