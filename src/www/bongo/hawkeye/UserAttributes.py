# a per-agent table of editable properties

import types
import bongo.admin.Schema as Schema
import bongo.admin.Util as Util

from bongo.bootstrap import mdb, msgapi

class HawkeyeUserAttributes:
    extraProps = {
        mdb.C_USER : [ mdb.A_GIVEN_NAME,
                       mdb.A_SURNAME ]
        }

    def __init__(self, type):
        self.constant = constant = []
        self.editable = editable = []

        editable.extend(self.extraProps.get(type, []))

    def GetConstant(self):
        return self.constant

    def GetEditable(self):
        return self.editable

class UserAttributes:
    def __init__(self, mdb, dn, values=None):
        self.schema = schema = Util.GetSchema()

        self.mdb = mdb
        self.dn = dn
        self.context, self.user = dn.rsplit("\\", 1)
        self.newValues = None

        if values:
            self.newValues = newValues = {}
            for key in values.keys():
                newValues[key] = values[key].value

        print "details for", dn
        details = mdb.GetObjectDetails(dn)

        self.type = type = details.get("Type")

        self.attrs = Util.GetUserAttributes(mdb, self.context, self.user)
        self.classAttrs = Util.GetClassAttributeObjects(type)
        self.typeAttrs = HawkeyeUserAttributes(type)

    def GetConstant(self):
        attrs = []

        constantAttrs = self.typeAttrs.GetConstant()

        for attr in constantAttrs:
            if not self.attrs.has_key(attr):
                continue

            id = attr
            name = Util.PropertiesPrintable.get(attr, attr)
            values = self.attrs.get(attr)

            if len(values) > 0:
                for value in values:
                    attrs.append((id, name, value))
            else:
                attrs.append((id, name, ""))

        return attrs

    def GetEditable(self):
        attrs = []

        editableAttrs = self.typeAttrs.GetEditable()

        for attr in editableAttrs:
            if not self.attrs.has_key(attr):
                continue

            id = attr
            name = Util.PropertiesPrintable.get(attr, attr)
            values = self.attrs.get(attr)

            if len(values) > 0:
                for value in values:
                    attrs.append((id, name, value))
            else:
                attrs.append((id, name, ""))

        return attrs

    def Validate(self):
        for key, value in self.newValues.iteritems():
            self.ValidateAttribute(key, value)
        return True

    def ValidateAttribute(self, attr, value):
        clsAttr = self.classAttrs.get(attr)

        if not clsAttr:
            raise ValueError("invalid attribute id: %s" % attr)

        return True

    def Save(self):
        attrs = {}

        for key, value in self.newValues.iteritems():
            if self.attrs.get(key) != value:
                attrs[key] = [value]

        Util.ModifyBongoUser(self.mdb, self.context, self.user, attrs)
        self.attrs = Util.GetUserAttributes(self.mdb, self.context, self.user)
