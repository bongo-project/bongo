from bongo.dragonfly.ResourceHandler import ResourceHandler
from bongo.store.StoreClient import StoreClient, DocTypes, DocFlags, FlagMode
from email.Utils import parseaddr
import bongo.commonweb

import math

class ContactsHandler(ResourceHandler):
    pageSize = 8
    def GetContactAddresses(contact):
        ret = []
        if contact.has_key("email"):
            for email in contact["email"]:
                if email.has_key("value"):
                    ret.append(email["value"])
        return ret
    GetContactAddresses = staticmethod(GetContactAddresses)

    def ParseAddresses(header):
        ret = []

        f = header.split("\n")
        for addr in f:
            addr = addr.strip()
            if addr == "":
                continue

            ret.append(parseaddr(addr))

        return ret
    ParseAddresses = staticmethod(ParseAddresses)

    def _SendContact(self, store, req, rp):
        contact = store.Info(rp.resource, ["nmap.document"])
        obj = self._JsonToObj(contact.props["nmap.document"])
        obj["bongoId"] = rp.resource
        obj["flags"] = self.DocumentFlags(contact.flags)

        return self._SendJson(req, obj)

    def _SendContacts(self, store, req, rp):
        collection = "/addressbook/" + "/".join(rp.collection)
        req.log.debug("listing contacts in %s", collection)

        if rp.page is None:
            start = end = -1
        else:
            start = rp.page * self.pageSize
            end = start + self.pageSize - 1

        contacts = store.List(collection, ["nmap.document"], start=start, end=end)

        contactList = []
        for contact in contacts:
            storeObj = self._JsonToObj(contact.props["nmap.document"])
            storeObj["bongoId"] = contact.uid
            storeObj["flags"] = self.DocumentFlags(contact.flags)
            contactList.append(storeObj)

        if rp.extension == "json":
            return self._SendJsonList(contactList, req, contacts.total)
        elif rp.extension == "vcf":
            return self._SendVcfList(contactList, req)

    def CompareContacts(a, b) :
        starreda = a["flags"].has_key("starred")
        starredb = b["flags"].has_key("starred")
        if starreda and not starredb :
            return -1
        elif starredb and not starredb :
            return 1
        
        ret = cmp(a["fn"], b["fn"])
        if ret != 0 :
            return ret
        
        return cmp(a["bongoId"], b["bongoId"])

    CompareContacts = staticmethod(CompareContacts)

    def _SearchContacts(self, store, req, rp):
        query = "nmap.type:addressbook %s" % rp.query
        req.log.debug("searching contacts for %s", rp.query)

        if rp.page is None:
            start = end = -1
        else:
            start = (rp.page - 1) * self.pageSize
            end = start + self.pageSize - 1

        contacts = []
        searchResults = store.ISearch(query, start=start, end=end)
        for contact in searchResults:
            contacts.append(contact)

        contactList = []
        for contact in contacts:
            doc = store.Read(contact.uid)
            storeObj = self._JsonToObj(doc)
            storeObj["bongoId"] = contact.uid
            storeObj["flags"] = self.DocumentFlags(contact.flags)
            contactList.append(storeObj)

        if rp.extension == "json":
            return self._SendJsonList(contactList, req, searchResults.total)
        elif rp.extension == "vcf":
            return self._SendVcfList(contactList, req)

    def _SendJsonList(self, objs, req, total=None):
        contactList = []
        for storeObj in objs:
            obj = {}
            obj["bongoId"] = storeObj["bongoId"]
            obj["flags"] = storeObj["flags"]

            if storeObj.has_key("fn"):
                obj["fn"] = storeObj["fn"]
            elif storeObj.has_key("n"):
                obj["fn"] = storeObj["n"][0]["value"]
            else:
                obj["fn"] = "No Name"

            for email in storeObj.get("email", []):
                obj.setdefault("email", []).append(email["value"])

            contactList.append(obj)

        if total is not None:
            ret = { "total": total,
                    "contacts": contactList }
        else:
            ret = contactList
        return self._SendJson(req, ret)

    def _SendVcfList(self, objs, req):
        req.content_type = "text/x-vcard"

        for obj in objs:
            self._Sendline(req, "BEGIN:VCARD")
            self._Sendline(req, "VERSION:3.0")

            if obj.has_key("fn"):
                self._Sendline(req, "FN:%s" % obj["fn"])

                names = obj["fn"].split(" ")
                names.reverse()
                self._Sendline(req, "N:%s" % ";".join(names))

            if obj.has_key("email"):
                for email in obj["email"]:
                    types = [";TYPE=%s" % emailType for emailType in email.get("type",[])]
                    self._Sendline(req, "EMAIL%s:%s"
                                    % (''.join(types), email["value"]))

            self._Sendline(req, "END:VCARD")
        
        return bongo.commonweb.OK

    def _Sendline(self, req, line):
        req.write(line)
        req.write("\r\n")

    def do_GET(self, req, rp):
        store = self.GetStoreClient(req, rp)
        try:
            if rp.resource is not None:
                return self._SendContact(store, req, rp)
            elif rp.query is not None:
                return self._SearchContacts(store, req, rp)
            else:
                return self._SendContacts(store, req, rp)
        finally:
            store.Quit()

    def _StarContacts(self, store, uids):
        for uid in uids:
            store.Flag(uid, DocFlags.Starred, FlagMode.Add)
        return bongo.commonweb.OK

    def _UnstarContacts(self, store, uids):
        for uid in uids:
            store.Flag(uid, DocFlags.Starred, FlagMode.Remove)
        return bongo.commonweb.OK

    def _DeleteContacts(self, store, uids):
        for uid in uids:
            store.Delete(uid)
        return bongo.commonweb.OK

    def do_POST(self, req, rp):
        length = int(req.headers_in["Content-Length"])
        req.log.debug("AddressbookView.do_POST (%d bytes)", length)
        doc = req.read(length)

        store = self.GetStoreClient(req, rp)
        try:
            json = self._JsonToObj(doc)
            method = json.get("method")

            if method == "createContact":
                return self._CreateContactReq(store, req, json, rp)
            elif method == "star" :
                return self._StarContacts(store, json.get("bongoIds"))
            elif method == "unstar" :
                return self._UnstarContacts(store, json.get("bongoIds"))
            elif method == "delete":
                return self._DeleteContacts(store, json.get("bongoIds"))
            return bongo.commonweb.HTTP_NOT_FOUND
        finally:
            store.Quit()

    def do_PUT(self, req, rp):
        length = int(req.headers_in["Content-Length"])
        req.log.debug("AddressbookView.do_PUT (%d bytes)", length)
        doc = req.read(length)

        store = self.GetStoreClient(req, rp)
        if rp.resource is not None:
            try:
                req.log.debug("Replacing doc %s", rp.resource)
                store.Replace(rp.resource, doc)
                return bongo.commonweb.OK
            finally:
                store.Quit()
        else:
            return bongo.commonweb.HTTP_NOT_FOUND

    def do_DELETE(self, req, rp):
        req.log.debug("AddressbookView.do_DELETE")
        if rp.resource is None :
            return bongo.commonweb.HTTP_NOT_FOUND

        store = self.GetStoreClient(req, rp)

        try :
            store.Delete(rp.resource)
            return bongo.commonweb.OK
        finally :
            store.Quit()
        
    def _CreateContact(self, store, req, rp, params):
        jsob, = params

        uid = store.Write("/addressbook/personal", DocTypes.Addressbook, self._ObjToJson(jsob))

        return {"bongoId":str(uid)}

    def _CreateContactReq(self, store, req, json, rp):
        ret = {"error" : None,
               "result" : None}

        try:
            req.log.debug("Writing new contact doc")
            result = self._CreateContact(store, req, rp, json.get("params"))
            ret["result"] = result
        except bongo.BongoError, e:
            ret["error"] = str(e)

        return self._SendJson(req, ret)
