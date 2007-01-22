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

import os, pwd
import Xpl

import logging
log = logging.getLogger("bongo.Privs")

def HaveBongoUser():
    if Xpl.BONGO_USER is None:
        return False
    return True

def GetBongoPwent():
    if not HaveBongoUser():
        return

    pwentry = pwd.getpwnam(Xpl.BONGO_USER)
    return pwentry[2], pwentry[3]  # uid, gid

def GetRootPwent():
    pwentry = pwd.getpwuid(0)
    return pwentry[2], pwentry[3]  # uid, gid

def Chown(filename):
    if not HaveBongoUser():
        return

    uid, gid = GetBongoPwent()

    s = os.stat(filename)
    if s[4] == uid:
        return

    log.debug("chowning %s to %s (uid %d)" % (filename, Xpl.BONGO_USER, uid))
    os.chown(filename, uid, gid)

def DropPrivs():
    if not HaveBongoUser():
        return

    uid, gid = GetBongoPwent()
    log.debug("dropping privileges: becoming %s (uid %d)" % (Xpl.BONGO_USER, uid))
    os.setegid(gid) # note: setegid has to happen before seteuid
    os.seteuid(uid)

def GainPrivs():
    if not HaveBongoUser():
        return

    uid, gid = GetRootPwent()
    log.debug("regaining privileges: becoming root")
    os.setegid(gid)
    os.seteuid(uid)

if __name__ == '__main__':
    print 'This file should not be executed directly; it is a library.'
