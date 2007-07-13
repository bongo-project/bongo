from bongo.store.CommandStream import CommandError
from bongo.store.StoreClient import StoreClient, DocTypes
from bongo.dragonfly.ResourceHandler import ResourceHandler
from bongo import BongoError
from bongo.dragonfly.HttpError import HttpError
from libbongo.libs import msgapi
import bongo.dragonfly

class DragonflyHandler(ResourceHandler):
    defaultPrefs = "{}"
    filename = "dragonfly"
            
    def do_GET(self, req, rp):
        # for preferences, require user == owner
        if req.user != rp.user:
            req.log.debug("Denying access to preferences of %s by %s" % (rp.user, req.user))
            raise HttpError (bongo.dragonfly.HTTP_FORBIDDEN)

        if rp.extension != 'json':
            req.log.debug("Invalid preferences extension: %s" % rp.extension)
            raise HttpError (bongo.dragonfly.HTTP_FORBIDDEN)

        store = self.GetStoreClient(req, rp)

        try:
            try:
                stream = store.Read("/preferences/%s" % self.filename)
            except CommandError, e:
                if e.code == 4240:
                    return bongo.dragonfly.Auth.DenyAccess (req)
                elif e.code == 4224:
                    stream = self.defaultPrefs
                else:
                    raise

            email = msgapi.GetUserEmailAddress(req.user)
            if email:
                name = msgapi.GetUserDisplayName(req.user)
                if name:
                    email = '%s <%s>' % (name, email)

                req.headers_out["X-Bongo-Address"] = email

            if rp.extension == "json":
                return self._SendJsonText(req, stream)

            req.write(stream)
            return bongo.dragonfly.OK
        finally:
            store.Quit()

    def do_PUT(self, req, rp):
        # for preferences, require user == owner
        if req.user != rp.user:
            req.log.debug("Denying access to preferences of %s by %s" % (rp.user, req.user))
            raise HttpError (bongo.dragonfly.HTTP_FORBIDDEN)

        if rp.extension != 'json':
            req.log.debug("Invalid preferences extension: %s" % rp.extension)
            raise HttpError (bongo.dragonfly.HTTP_FORBIDDEN)
        
        length = int(req.headers_in["Content-Length"])
        req.log.debug("PreferencesView.do_PUT (%d bytes)", length)

        doc = req.read(length)
        try:
            jsob = self._JsonToObj(doc)
        except Exception, e:
            req.log.debug("Invalid JSON: %s: %s" % (e, doc))
            raise HttpError (bongo.dragonfly.HTTP_BAD_REQUEST)

        doc = self._ObjToJson(jsob)
        store = self.GetStoreClient(req, rp)
        try:
            req.log.debug("Replacing doc %s", self.filename)
            item = store.Info("/preferences/%s" % self.filename)
            store.Replace(item.uid, doc)
        except CommandError, e:
            if e.code == 4240:
                store.Quit()
                return bongo.dragonfly.Auth.DenyAccess (req)
            elif e.code == 4224:
                req.log.debug("Writing new doc %s", self.filename)
                store.Write("/preferences", DocTypes.Unknown, doc, self.filename)
                store.Quit()
                return bongo.dragonfly.OK
            else:
                store.Quit()
                raise
        except Exception, e:
            req.log.debug("Could not save preferences: %s" % e)
            store.Quit()
            raise

class RulesHandler(ResourceHandler):
    ConditionTypes = {
        "any" : { "rule" : 'A',
                  "notrule" : None,
                  "arg1" : None,
                  "arg2" : None },
        "from" : { "rule" : 'B',
                   "notrule" : 'H',
                   "arg1" : "string",
                   "arg2" : None },
        "to" : { "rule" : 'C',
                 "notrule" : 'I',
                 "arg1" : "string",
                 "arg2" : None },
        "subject" : { "rule" : 'D',
                      "notrule" : 'J',
                      "arg1" : "string",
                      "arg2" : None },
        "priority" : { "rule" : 'E',
                       "notrule" : 'K',
                       "arg1" : "string",
                       "arg2" : None },
        "header" : { "rule" : 'F',
                     "notrule" : 'L',
                     "arg1" : "header",
                     "arg2" : "string" },
        "body" : { "rule": 'G',
                   "notrule" : 'M',
                   "arg1" : "string",
                   "arg2" : None },
        "sizemore" : { "rule": 'N',
                       "notrule" : None,
                       "arg1" : "size",
                       "arg2" : None },
        "sizeless" : { "rule": 'O',
                       "notrule" : None,
                       "arg1" : "size",
                       "arg2" : None },
        "hasmimetype" : { "rule" : 'f',
                          "notrule" : 'g',
                          "arg1" : "mimetype",
                          "arg2" : None },
        }

    ActionTypes = {
        "reply" : { "rule" : 'A',
                    "arg1" : "filename"
                    },
        "delete" : { "rule" : 'B',
                     "arg1" : None },
        "forward" : { "rule" : 'C',
                      "arg1" : "address" },
        "cc" : { "rule" : 'D',
                 "arg1" : "address" },
        "move" : { "rule" : 'E',
                   "arg1" : "folder" },
        "stop" : { "rule" : 'F',
                   "arg1" : None },
        "bounce" : { "rule" : 'I',
                     "arg1" : None },
        }
                    

    BoolOperators = {
        "or" : "Y",
        "and" : "Y",
        }
        
    def do_GET(self, req, rp):
        dn = msgapi.FindObject(req.user)
        mdbrules = msgapi.GetUserFeature(dn, "rules", "BongoRule")
        if not mdbrules :
            mdbrules = []
        
        return bongo.dragonfly.OK

    def _StringToRule(self, string) :
        if string is None :
            return "000Z"
        else :
            return "%03d%sZ" % (len(string), string)

    def _ConditionToRule(self, condition) :        
        conditionType = self.ConditionTypes.get(condition.get("type"))
        if not conditionType :
            return None

        if condition.get("negate") is not False or conditionType["notrule"] is None:
            rule = conditionType["rule"]
        else :
            rule = conditionType["notrule"]

        rule = rule + self._StringToRule(condition.get(conditionType["arg1"]))
        rule = rule + self._StringToRule(condition.get(conditionType["arg2"]))
        return rule

    def _ActionToRule(self, action) :
        actionType = self.ActionTypes.get(action.get("type"))
        if not actionType :
            return None

        rule = actionType["rule"]
        rule = rule + self._StringToRule(action.get(actionType["arg1"]))
        return rule
        
    def _GetId(self) :
        # FIXME
        return "xxxxxxx"

    def _RuleToMdb(self, rule) :
        if rule.get("id") :
            mdbRule = rule["id"]
        else :
            mdbRule = self._GetId()

        if rule.get("active") is False :
            mdbRule = mdbRule + "B"
        else :
            mdbRule = mdbRule + "A"

        first = True
        for condition in rule["conditions"] :
            condstr = self._ConditionToRule(condition)

            if condstr :
                if not first :
                    mdbRule = mdbRule + self.BoolOperators[condition["operator"]]
                mdbRule = mdbRule + condstr
                first = False
        for action in rule["actions"] :
            actionstr = self._ActionToRule(action)

            if actionstr :
                mdbRule = mdbRule + actionstr

        print "returning %s" % (mdbRule)
        return mdbRule

    def do_PUT(self, req, rp):
        length = int(req.headers_in["Content-Length"])
        jsob = self._JsonToObj(req.read(length))

        rules = []
        for rule in jsob :
            rules.append(self._RuleToMdb(rule))

        print "\r\n".join(rules)

    def do_POST(self, req, rp) :
        return self.do_PUT(req, rp)
