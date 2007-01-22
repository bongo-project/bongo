import os
#from mod_python import psp
#from mod_python import apache

# Wrapper for using PSP as a templating system.
class Template:
    def __init__(self, filename):
        self.pagevars = {}
        self.filename = filename

    def SetPageVariable(self, name, value):
        self.pagevars[name] = value

    def GetPageVariable(self, name):
        return self.pagevars[name]

    def SendPage(self, req):
        req.content_type ='text/html'
        self.pagevars['base'] = req.application_path
        filename = os.path.join(req.filesystem_path, self.filename)
        #tmpl = psp.PSP(req, filename=filename)
        #tmpl.run(self.pagevars)
        f = open(filename, 'r')
        html = f.read()
        f.close()
        for key in self.pagevars:
            html = html.replace('<%=' + key + '%>', self.pagevars[key])
        req.write(html)
