/*------------------------------------------------------------------------------
* Copyright (C) 2003-2006 Ben van Klinken and the CLucene Team
* 
* Distributable under the terms of either the Apache License (Version 2.0) or 
* the GNU Lesser General Public License, as specified in the COPYING file.
------------------------------------------------------------------------------*/
#include "CLucene/StdHeader.h"
#include "Explanation.h"
#include "CLucene/util/StringBuffer.h"

CL_NS_USE(util)
CL_NS_DEF(search)


Explanation::Explanation(const Explanation& copy){
    this->value = copy.value;
    STRCPY_TtoT(description,copy.description,LUCENE_SEARCH_EXPLANATION_DESC_LEN);

    CL_NS(util)::CLArrayList<Explanation*,CL_NS(util)::Deletor::Object<Explanation> >::iterator itr;
    itr = details.begin();
    while ( itr != details.end() ){
        details.push_back( (*itr)->clone() );
        itr++;
    }
}

Explanation::~Explanation(){
}

void Explanation::setDescription(TCHAR* description) {
   _tcsncpy(this->description,description,LUCENE_SEARCH_EXPLANATION_DESC_LEN);
}


Explanation* Explanation::clone() const{ 
   return _CLNEW Explanation(*this); 
}

float_t Explanation::getValue() { 
   return value; 
}
  
void Explanation::setValue(float_t value) { 
   this->value = value; 
}

TCHAR* Explanation::getDescription() { 
   return description; 
}

///todo: mem leaks
TCHAR* Explanation::toString(int32_t depth) {
 StringBuffer buffer;
 for (int32_t i = 0; i < depth; i++) {
   buffer.append(_T("  "));
 }
 buffer.appendFloat(getValue(),2);
 buffer.append(_T(" = "));
 buffer.append(getDescription());
 buffer.append(_T("\n"));

 Explanation** details = getDetails();
 int32_t j =0;
 while ( details[j] != NULL ){
   TCHAR* tmp = details[j]->toString(depth+1);
   buffer.append(tmp);
   _CLDELETE_CARRAY(tmp);

   _CLDELETE(details[j]);
   j++;
 }
 _CLDELETE_ARRAY(details);
 return buffer.toString();
}

Explanation::Explanation(float_t value, TCHAR* description) {
 this->value = value;
 _tcsncpy(this->description,description,LUCENE_SEARCH_EXPLANATION_DESC_LEN);
}

/** The sub-nodes of this explanation node. */
Explanation** Explanation::getDetails() {
 Explanation** ret = _CL_NEWARRAY(Explanation*,details.size()+1);
 for ( uint32_t i=0;i<details.size();i++ ){
   ret[i] = details[i]->clone();
 }
 ret[details.size()] = NULL;
 return ret;
}

/** Adds a sub-node to this explanation node. */
void Explanation::addDetail(Explanation* detail) {
   details.push_back(detail);
}

/** Render an explanation as text. */
TCHAR* Explanation::toString() {
 return toString(0);
}

/** Render an explanation as HTML. */
///todo: mem leaks
TCHAR* Explanation::toHtml() {
 StringBuffer buffer;
 TCHAR* tmp;
 buffer.append(_T("<ul>\n"));

 buffer.append(_T("<li>"));
 buffer.appendFloat(getValue(),2);
 buffer.append(_T(" = "));
 
 buffer.append(getDescription());
 buffer.append(_T("</li>\n"));

 Explanation** details = getDetails();
 int32_t i =0;
 while ( details[i] != NULL ){
    tmp = details[i]->toHtml();
    buffer.append(tmp);
    _CLDELETE_CARRAY(tmp);
    _CLDELETE(details[i]);
    i++;
 }
 _CLDELETE_ARRAY(details);
 
 buffer.append(_T("</ul>\n"));

 return buffer.toString();
}
CL_NS_END
