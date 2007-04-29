import os, sys, copy
from bongo.external.simpletal import simpleTAL, simpleTALES

class Template:
    def __init__(self, depth=0):
        self._context = simpleTALES.Context()
        self._depth = depth
        self._templatefile = None
        self._preprocessor = TemplatePreprocessor()

    def Run(self, req):
        if self._templatefile == None:
            raise RuntimeError
        
        self._preprocessor.setTemplateFile(self._templatefile)
        template = simpleTAL.compileHTMLTemplate(self._preprocessor)
        template.expand(self._context, req)

    def setVariable(self, name, obj):
        self._context.addGlobal(name, obj)
        self._preprocessor[name] = obj

    def setTemplateFile(self, filename):
        self._templatefile = filename

    def setTemplatePath(self, path):
        self._templatepath = path
        self._preprocessor.setTemplatePath(path)

    def setTemplateUriRoot(self, root):
        self._templateuriroot = root
        self._preprocessor.setUriRoot(root)

class TemplatePreprocessor:
    def __init__(self, dict={}):
        self.dict = dict

    def read(self):
        return "%s" % self

    def setTemplateFile(self, filename):
        content = self._filecontent(filename)
        if content != "":
            self._template = content
        else:
            self._template = "<html><body><h1>Error</h1><p>No template %s</p></body></html>" % filename

    def __str__(self):
        # rely on string substitutions
        return self._template % self

    def __getitem__(self, key):
        return self._process(key.split("|"))

    def __setitem__(self, key, value):
        self.dict[key] = value

    def _process(self, args):
        arg = args.pop(0)
        if len(args) == 0:
            if arg in self.dict:
                return self.dict[arg]
            elif hasattr(self, "call_" + arg) and callable(getattr(self, "call_" + arg)):
                return getattr(self, "call_" + arg)()
            else:
                return "Bad attribute %s" % arg
        else:
            return getattr(self, "call_" + arg)(args)

    def _filecontent(self, filename):
        content = ""
        try:
            f = open (filename, "r")
            content = f.read()
            f.close()
        except:
            print "Couldn't open file %s" % filename
        return content
    
    def setTemplatePath(self, path):
        self._templatepath = path

    def setUriRoot(self, root):
        self._uriroot = root

    # template callable functions
    def call_url(self, args):
        # Pad out URLs with our web root, so the app can be moved in terms of 
        # URL and the links all still work
        return self._uriroot + args[0]

    def call_include(self, args):
        # Include a copy of another template here. This is also processed.
        template = self._templatepath + '/' + args[0]
        pp = copy.copy(self)
        pp.setTemplateFile(template)
        return pp.read()
