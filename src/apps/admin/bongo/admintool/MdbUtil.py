import os, re

import bongo.Xpl as Xpl

def GetSetupMdb(options):
    from bongo.MDB import MDB
    conf = ReadMdbConf()

    # Before an MDBFILE tree is set up, we can't auth against it
    if conf.get("driver") == "mdbfile":
        return MDB(None, None)

    return MDB(options.authuser, options.authpass)

def GetMdb(options, inSetup=False):
    from bongo.MDB import MDB

    user = '\\'.join(GetContext(options.authuser))

    return MDB(user, options.authpass)

def GetContext(mdbDn, defContext=None):
    from bongo.libs import msgapi

    if '\\' in mdbDn:
        return mdbDn.rsplit('\\', 1)

    if defContext is None:
        defContext = msgapi.GetConfigProperty(msgapi.DEFAULT_CONTEXT)

    return defContext, mdbDn

def GetDefaultContext(options):
    if options.context:
        return options.context

    # Otherwise, use the current user's context
    context, user = GetContext(options.authuser)
    return context

def ReadMdbConf(mdbfile=None):
    if mdbfile is None:
        mdbfile = os.path.join(Xpl.DEFAULT_CONF_DIR, "mdb.conf")

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
