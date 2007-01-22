# a per-agent table of editable properties

import types
import bongo.admin.Schema as Schema
import bongo.admin.Util as Util

from bongo.bootstrap import mdb, msgapi

class MultilineStringType(types.StringType):
    pass

class MdbPathType(types.StringType):
    pass

class Attribute:
    def __init__(self, type):
        self.type = type
        self.name = Util.PropertyArguments.get(type)
        self.prettyName = Util.PropertiesPrintable.get(type)
    
class EditableAttribute(Attribute):
    def Validate(self, data):
        pass

HawkeyeAttributes = {
    msgapi.A_BOUNCE_CC_POSTMASTER : EditableAttribute(types.BooleanType),
    msgapi.A_BOUNCE_RETURN : EditableAttribute(types.BooleanType),
    msgapi.A_CLIENT : EditableAttribute(MdbPathType),
    msgapi.A_DOMAIN : EditableAttribute(types.StringType),
    msgapi.A_MAXIMUM_ITEMS : EditableAttribute(types.IntType),
    msgapi.A_MESSAGE_LIMIT : EditableAttribute(types.IntType),
    msgapi.A_MESSAGE_STORE : EditableAttribute(MdbPathType),
    msgapi.A_MINIMUM_SPACE : EditableAttribute(types.IntType),
    msgapi.A_MONITORED_QUEUE : EditableAttribute(MdbPathType),
    msgapi.A_PORT : EditableAttribute(types.IntType),
    msgapi.A_QUEUE_INTERVAL : EditableAttribute(types.IntType),
    msgapi.A_QUEUE_SERVER : EditableAttribute(MdbPathType),
    msgapi.A_QUEUE_TIMEOUT : EditableAttribute(types.IntType),
    msgapi.A_QUOTA_MESSAGE : EditableAttribute(MultilineStringType),
    msgapi.A_ROUTING : EditableAttribute(types.BooleanType),
    msgapi.A_SCMS_DIRECTORY : EditableAttribute(types.StringType),
    msgapi.A_SCMS_SIZE_THRESHOLD : EditableAttribute(types.IntType),
    msgapi.A_SCMS_USER_THRESHOLD : EditableAttribute(types.IntType),
    msgapi.A_SMTP_ACCEPT_ETRN : EditableAttribute(types.BooleanType),
    msgapi.A_SMTP_SEND_ETRN : EditableAttribute(types.BooleanType),
    msgapi.A_SPOOL_DIRECTORY : EditableAttribute(types.StringType),
    msgapi.A_SSL_PORT : EditableAttribute(types.IntType),
    msgapi.A_STORE_SERVER : EditableAttribute(MdbPathType),
    msgapi.A_TIMEOUT : EditableAttribute(types.IntType),
    msgapi.A_TIME_INTERVAL : EditableAttribute(types.IntType),
    msgapi.A_UBE_REMOTE_AUTH_ONLY : EditableAttribute(types.BooleanType),
    msgapi.A_UBE_SMTP_AFTER_POP : EditableAttribute(types.BooleanType),
    }

class HawkeyeAttributes:
    extraProps = {
        msgapi.C_ADDRESSBOOK : [ msgapi.A_MONITORED_QUEUE,
                                 msgapi.A_PORT ],

        msgapi.C_ANTISPAM : [ msgapi.A_MONITORED_QUEUE, ],

        msgapi.C_CONNMGR : [ msgapi.A_TIMEOUT ],

        msgapi.C_IMAP : [ msgapi.A_PORT,
                          msgapi.A_SSL_PORT,
                          msgapi.A_MONITORED_QUEUE ],

        msgapi.C_POP : [ msgapi.A_PORT,
                         msgapi.A_SSL_PORT,
                         msgapi.A_MONITORED_QUEUE ],

        msgapi.C_PROXY : [ msgapi.A_MAXIMUM_ITEMS,
                           msgapi.A_TIME_INTERVAL,
                           msgapi.A_MONITORED_QUEUE,
                           msgapi.A_STORE_SERVER ],

        msgapi.C_QUEUE : [ msgapi.A_BOUNCE_RETURN,
                           msgapi.A_BOUNCE_CC_POSTMASTER,
                           msgapi.A_SPOOL_DIRECTORY,
                           msgapi.A_QUOTA_MESSAGE,
                           msgapi.A_QUEUE_TIMEOUT,
                           msgapi.A_QUEUE_INTERVAL,
                           msgapi.A_MINIMUM_SPACE,
                           msgapi.A_CLIENT ],

        msgapi.C_SMTP : [ msgapi.A_DOMAIN,
                          msgapi.A_MESSAGE_LIMIT,
                          msgapi.A_MONITORED_QUEUE,
                          msgapi.A_QUEUE_SERVER,
                          msgapi.A_PORT,
                          msgapi.A_SSL_PORT,
                          msgapi.A_UBE_REMOTE_AUTH_ONLY,
                          msgapi.A_ROUTING,
                          msgapi.A_SMTP_ACCEPT_ETRN,
                          msgapi.A_SMTP_SEND_ETRN,
                          msgapi.A_UBE_SMTP_AFTER_POP ],

        msgapi.C_STORE : [ msgapi.A_CLIENT,
                           msgapi.A_MESSAGE_STORE,
                           msgapi.A_MINIMUM_SPACE,
                           msgapi.A_SCMS_USER_THRESHOLD,
                           msgapi.A_SCMS_SIZE_THRESHOLD,
                           msgapi.A_SCMS_DIRECTORY ]
        }

    def __init__(self, type):
        self.editable = editable = []
        self.constant = constant = []

        self.editable.extend(self.extraProps.get(type, []))

    def GetConstant(self):
        return self.constant

    def GetEditable(self):
        return self.editable

class AgentAttributes:
    def __init__(self, mdb, dn, values=None):
        self.schema = schema = Util.GetSchema()

        self.mdb = mdb
        self.dn = dn
        self.server, self.agent = dn.split("\\")
        self.newValues = None

        if values:
            self.newValues = self.CollapseValues(values)

        details = Util.GetObjectDetails(mdb, dn)

        self.agentType = agentType = details.get("Type")

        self.attrs = Util.GetAgentAttributes(mdb, dn)
        self.classAttrs = Util.GetClassAttributeObjects(agentType)
        self.typeAttrs = HawkeyeAttributes(agentType)
        self.disabled = self.attrs.get(msgapi.A_DISABLED)

    def CollapseValues(self, values):
        """Turn a FieldStorage object into a hash"""
        ret = {}

        for key in values.keys():
            ret[key] = values.getlist(key)

        return ret

    def IsEnabled(self):
        if len(self.disabled) and self.disabled[0]:
            return False
        return True

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
        Util.ModifyBongoAgent(self.mdb, self.server, self.agent, self.newValues)
        self.attrs = Util.GetAgentAttributes(self.mdb, self.dn)
