import bongo.external.simplejson as simplejson

class Contact:
    collapsed = ["fn", "note"]
    imFields = {"x-aim" : "aim",
                "x-groupwise" : "groupwise",
                "x-icq" : "icq"}

    def __init__(self, obj):
        self._FillFromVcard(obj)

    def _SetChild(self, data, content):
        if content.value == "":
            return

        field = content.name.lower()

        if field in self.collapsed:
            data[field] = content.value
            return

        obj = {}

        if field in self.imFields:
            obj["subtype"] = self.imFields[field]
            field = "im"

        obj["value"] = str(content.value)

        for key,value in content.params.items():
            obj[key.lower()] = value
        
        data.setdefault(field, []).append(obj)

    def _FillFromVcard(self, obj):
        # Convert a vobject.vcard to our Json format
        data = {}

        for c in obj.getChildren():
            self._SetChild(data, c)

        self.data = data

    def AsJson(self):
        return simplejson.dumps(self.data)

    def AsVcard(self):
        pass

