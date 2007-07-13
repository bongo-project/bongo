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

from libbongo.bootstrap import mdb
from bongo.BongoError import BongoError


class MDBError(BongoError):
    pass

class MDB(mdb):
    def __init__(self, username, password):
        mdb.mdb_Init()
        self.handle = self.mdb_Authenticate('Bongo', username, password) 
        if self.handle is None:
            raise MDBError('Authentication failure')
        self.baseDn = mdb.mdb_GetBaseDN(self.handle)

    def __del__(self):
        self.mdb_Release(self.handle)
        self.mdb_Shutdown()

    def GetAttributes(self, obj, attrname):
        values = []
        if not self.IsObject(obj):
            raise MDBError('Object %s does not exist' % obj)
        vs = self.mdb_CreateValueStruct(self.handle)
        count = self.mdb_Read(self.handle, obj, attrname, vs)
        for i in range(0, count):
            values.append(self.mdb_VSGetValue(vs, i))
        self.mdb_DestroyValueStruct(vs)
        return values

    def GetFirstAttribute(self, obj, attrname, default=None):
        value = default
        vs = self.mdb_CreateValueStruct(self.handle)
        count = self.mdb_Read(self.handle, obj, attrname, vs)
        if count > 0:
            value = self.mdb_VSGetValue(vs, 0)
        self.mdb_DestroyValueStruct(vs)
        return value

    def GetDNAttributes(self, obj, attrname):
        vs = self.mdb_CreateValueStruct(self.handle)
        count = self.mdb_ReadDN(self.handle, obj, attrname, vs)
        attributes = []
        for i in range(0, count):
            attributes.append(self.mdb_VSGetValue(vs, i))
        self.mdb_DestroyValueStruct(vs)
        return attributes

    def AddAttribute(self, obj, attrname, value):
        if '\\' in value:
            success = self.mdb_AddDN(self.handle, obj, attrname, value)
        else:
            success = self.mdb_Add(self.handle, obj, attrname, value)
        if not success:
            raise MDBError('Could not add attribute ' + attrname)
        return

    def SetAttribute(self, obj, attrname, vallist):
        useDN = False
        vs = self.mdb_CreateValueStruct(self.handle)
        for val in vallist:
            if '\\' in val:
                useDN = True
            self.mdb_VSAddValue(val, vs)
        if useDN:
            success = self.mdb_WriteDN(self.handle, obj, attrname, vs)
        else:
            success = self.mdb_Write(self.handle, obj, attrname, vs)
        if not success:
            extmsg = ' on ' + obj + '\n'
            for val in vallist:
                extmsg += ' ' + val + '\n'
            raise MDBError('Could not set attribute ' + attrname + extmsg)
        return

    #attrdict = {'key1':['val1'], 'key2':['valA', 'valB']}
    def AddObject(self, obj, classname, attrdict):
        vsattr = self.mdb_CreateValueStruct(self.handle)
        vsdata = self.mdb_CreateValueStruct(self.handle)
        for key, vals in attrdict.iteritems():
            if '\\' in vals[0]:
                key = ('%c' % self.MDB_ATTR_SYN_DIST_NAME) + key
            else:
                key = ('%c' % self.MDB_ATTR_SYN_STRING) + key
            for val in vals:
                self.mdb_VSAddValue(key, vsattr)
                self.mdb_VSAddValue(val, vsdata)
        success = self.mdb_CreateObject(self.handle, obj, classname, \
                                       vsattr, vsdata)
        self.mdb_DestroyValueStruct(vsattr)
        self.mdb_DestroyValueStruct(vsdata)
        if not success:
            raise MDBError('Could not create object ' + obj)
        return

    def DeleteObject(self, obj, recursive = False):
        if not self.mdb_DeleteObject(self.handle, obj, recursive):
            raise MDBError('Could not delete object ' + obj)
        return

    def SetPassword(self, obj, password):
        if len(password) > 127:
            raise MDBError('Password too long')
        if not self.mdb_ChangePasswordEx(self.handle, obj, None, password):
            raise MDBError('Could not set password on %s' % obj)
        return

    def VerifyPassword(self, obj, password):
        return  self.mdb_VerifyPassword(self.handle, obj, password)

    def IsObject(self, obj):
        success = self.mdb_IsObject(self.handle, obj)
        return success

    def EnumerateObjects(self, container, classname, pattern = None):
        objlist = []
        vs = self.mdb_CreateValueStruct(self.handle)
        if not self.mdb_VSSetContext(container, vs):
            raise MDBError('Could not set context to ' + container)
        count = self.mdb_EnumerateObjects(self.handle, container,
                                          classname, pattern, vs)
        for i in range(0, count):
            objlist.append(self.mdb_VSGetValue(vs, i))
        self.mdb_DestroyValueStruct(vs)
        return objlist

    def EnumerateAttributes(self, obj):
        attrlist = []
        vs = self.mdb_CreateValueStruct(self.handle)
        es = self.mdb_CreateEnumStruct(self.handle, vs)
        while True:
            attr = self.mdb_EnumerateAttributesEx(self.handle, obj, es, vs)
            if attr == None:
                break
            attrlist.append(attr)
        self.mdb_DestroyEnumStruct(self.handle, es, vs)
        self.mdb_DestroyValueStruct(vs)
        return attrlist

    #optdict = { 'type':'dn', 'singleval':True, ... }
    def DefineAttribute(self, attrname, asn1, optdict):
        attrtype = self.MDB_ATTR_SYN_STRING
        single = False
        sync = False
        public = False
        for key, val in optdict.iteritems():
            if key.lower() == 'type':
                if val.lower() == 'dn':
                    attrtype = self.MDB_ATTR_SYN_DIST_NAME
            elif key.lower() == 'singleval':
                if val == True:
                    attrtype = True
            elif key.lower() == 'immsync':
                if val == True:
                    sync = True
            elif key.lower() == 'public':
                if val == True:
                    sync = True
            if not self.mdb_DefineAttribute(self.handle, attrname, asn1, \
                                           attrtype, single, sync, public):
                raise MDBError('Could not define attribute ' + attrname)
        return

    #optdict = { 'container':[True], 'superclasses':['class1', 'class2'], ... }
    def DefineClass(self, classname, asn1, optdict):
        container = False
        vssuper = self.mdb_CreateValueStruct(self.handle)
        vscont = self.mdb_CreateValueStruct(self.handle)
        vsnam = self.mdb_CreateValueStruct(self.handle)
        vsmand = self.mdb_CreateValueStruct(self.handle)
        vsopt = self.mdb_CreateValueStruct(self.handle)
        #fixme: partial success cases
        for key, vals in optdict.iteritems():
            if key.lower() == 'container':
                if vals[0] == True:
                    container = True
            if key.lower() == 'superclasses':
                for val in vals:
                    if not self.mdb_VSAddValue(val, vssuper):
                        raise MDBError('Could not add superclass value ' + val)
            if key.lower() == 'containers':
                for val in vals:
                    if not self.mdb_VSAddValue(val, vscont):
                        raise MDBError('Could not add containers value ' + val)
            if key.lower() == 'naming':
                if not self.mdb_VSAddValue(vals[0], vsnam):
                    raise MDBError('Could not add naming value ' + val)
            if key.lower() == 'requires':
                for val in vals:
                    if not self.mdb_VSAddValue(val, vsmand):
                        raise MDBError('Could not add requires value ' + val)
            if key.lower() == 'allows':
                for val in vals:
                    if not self.mdb_VSAddValue(val, vsopt):
                        raise MDBError('Could not add allows value ' + val)
        if self.mdb_VSGetUsed(vssuper) > 0:
            success = self.mdb_DefineClass(self.handle, classname, asn1, \
                                      container, vssuper, vscont, vsnam, \
                                      vsmand, vsopt)
        self.mdb_DestroyValueStruct(vssuper)
        self.mdb_DestroyValueStruct(vscont)
        self.mdb_DestroyValueStruct(vsnam)
        self.mdb_DestroyValueStruct(vsmand)
        self.mdb_DestroyValueStruct(vsopt)
        if not success:
            if self.mdb_VSGetUsed(vssuper) > 0:
                raise MDBError('Could not define class ' + classname)
            else:
                raise MDBError('No superclass specified')
        return

    def DefineClassAttribute(self, classname, attrname):
        if not self.mdb_AddAttribute(self.handle, attrname, classname):
            raise MDBError('Could not define class attribute ' + attrname)
        return

    def DeleteAttributeDefinition(self, attrname):
        if not self.mdb_UndefineAttribute(self.handle, attrname):
            raise MDBError('Could not delete attribute definition ' + attrname)
        return

    def DeleteClassDefinition(self, classname):
        if not self.mdb_UndefineClass(self.handle, classname):
            raise MDBError('Could not delete class definition ' + classname)
        return

    #optdict = { 'read':True, 'write':True, ... }
    def GrantObject(self, obj, trustee, optdict):
        read = False
        write = False
        delete = False
        rename = False
        admin = False
        for key, val in optdict.iteritems():
            if key.lower() == 'read':
                if val == True:
                    read = True
            if key.lower() == 'write':
                if val == True:
                    write = True
            if key.lower() == 'delete':
                if val == True:
                    delete = True
            if key.lower() == 'rename':
                if val == True:
                    rename = True
            if key.lower() == 'admin':
                if val == True:
                    admin = True
        if not self.mdb_GrantObjectRights(self.handle, obj, trustee, \
                                         read, write, delete, rename, admin):
            raise MDBError('Could not grant object rights on ' + obj)
        return

    #optdict = { 'read':True, 'write':True, ... }
    def GrantAttribute(self, obj, attrname, trustee, optdict):
        read = False
        write = False
        admin = False
        for key, val in optdict.iteritems():
            if key.lower() == 'read':
                if val == True:
                    read = True
            if key.lower() == 'write':
                if val == True:
                    write = True
            if key.lower() == 'admin':
                if val == True:
                    admin = True
        if not self.mdb_GrantAttributeRights(self.handle, obj, attrname, \
                                            trustee, read, write, admin):
            raise MDBError('Could not grant attribute rights on ' + attrname)
        return

    def GetObjectDetails(self, obj):
        details = {}
        if not self.IsObject(obj):
            raise MDBError('Cannot get details of nonexistent object ' + obj)
        details['Type'] = self.mdb_GetObjectDetails_Type(self.handle, obj)
        details['RDN'] = self.mdb_GetObjectDetails_RDN(self.handle, obj)
        details['DN'] = self.mdb_GetObjectDetails_DN(self.handle, obj)
        return details

if __name__ == '__main__':
    print 'This file should not be executed directly; it is a library.'
