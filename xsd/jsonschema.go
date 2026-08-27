package xsd

import (
	"fmt"
	"sync"

	"github.com/knroy/go-xml/xdm"
)

// The schema for the XML representation of JSON.
//
// F&O 3.1 §C.2 ("Schema for the result of fn:json-to-xml") gives this schema
// in full, and §17.5.3 makes it the one thing a processor must validate
// against when fn:json-to-xml is called with validate=true: the option
// "indicates that the resulting XDM instance must be typed; that is, the
// element and attribute nodes must carry the type annotations that result
// from validation against the schema given at C.2".
//
// It is built in rather than fetched because the specification names it
// rather than locating it: a stylesheet writes
// <xsl:import-schema namespace="http://www.w3.org/2005/xpath-functions"/>
// with no schema-location and expects the processor to already have it —
// XSLT 3.0 §3.14 says an import with no location "indicates that the
// processor is expected to have the required schema components available",
// and this is the one schema every 3.0 processor is expected to have.
// json-to-xml-typed-001 to -007 are that stylesheet.

// jsonSchemaSource is F&O 3.1 §C.2 verbatim, less the xs:annotation elements
// that only carry the W3C licence text.
const jsonSchemaSource = `<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema"
    elementFormDefault="qualified"
    targetNamespace="http://www.w3.org/2005/xpath-functions"
    xmlns:j="http://www.w3.org/2005/xpath-functions">

    <xs:element name="map" type="j:mapType">
        <xs:unique name="unique-key">
            <xs:selector xpath="*"/>
            <xs:field xpath="@key"/>
        </xs:unique>
    </xs:element>

    <xs:element name="array" type="j:arrayType"/>

    <xs:element name="string" type="j:stringType"/>

    <xs:element name="number" type="j:numberType"/>

    <xs:element name="boolean" type="xs:boolean"/>

    <xs:element name="null" type="j:nullType"/>

    <xs:complexType name="nullType">
        <xs:sequence/>
    </xs:complexType>

    <xs:complexType name="stringType">
        <xs:simpleContent>
            <xs:extension base="xs:string">
                <xs:attribute name="escaped" type="xs:boolean" use="optional" default="false"/>
            </xs:extension>
        </xs:simpleContent>
    </xs:complexType>

    <xs:simpleType name="numberType">
        <xs:restriction base="xs:double">
            <xs:minExclusive value="-INF"/>
            <xs:maxExclusive value="INF"/>
        </xs:restriction>
    </xs:simpleType>

    <xs:complexType name="arrayType">
        <xs:choice minOccurs="0" maxOccurs="unbounded">
            <xs:element ref="j:map"/>
            <xs:element ref="j:array"/>
            <xs:element ref="j:string"/>
            <xs:element ref="j:number"/>
            <xs:element ref="j:boolean"/>
            <xs:element ref="j:null"/>
        </xs:choice>
    </xs:complexType>

    <xs:complexType name="mapType">
        <xs:choice minOccurs="0" maxOccurs="unbounded">
            <xs:element name="map">
                <xs:complexType>
                    <xs:complexContent>
                        <xs:extension base="j:mapType">
                            <xs:attribute name="key" type="xs:string"/>
                        </xs:extension>
                    </xs:complexContent>
                </xs:complexType>
                <xs:unique name="unique-key-2">
                    <xs:selector xpath="*"/>
                    <xs:field xpath="@key"/>
                </xs:unique>
            </xs:element>
            <xs:element name="array">
                <xs:complexType>
                    <xs:complexContent>
                        <xs:extension base="j:arrayType">
                            <xs:attributeGroup ref="j:key-group"/>
                        </xs:extension>
                    </xs:complexContent>
                </xs:complexType>
            </xs:element>
            <xs:element name="string">
                <xs:complexType>
                    <xs:simpleContent>
                        <xs:extension base="j:stringType">
                            <xs:attributeGroup ref="j:key-group"/>
                        </xs:extension>
                    </xs:simpleContent>
                </xs:complexType>
            </xs:element>
            <xs:element name="number">
                <xs:complexType>
                    <xs:simpleContent>
                        <xs:extension base="j:numberType">
                            <xs:attributeGroup ref="j:key-group"/>
                        </xs:extension>
                    </xs:simpleContent>
                </xs:complexType>
            </xs:element>
            <xs:element name="boolean">
                <xs:complexType>
                    <xs:simpleContent>
                        <xs:extension base="xs:boolean">
                            <xs:attributeGroup ref="j:key-group"/>
                        </xs:extension>
                    </xs:simpleContent>
                </xs:complexType>
            </xs:element>
            <xs:element name="null">
                <xs:complexType>
                    <xs:attributeGroup ref="j:key-group"/>
                </xs:complexType>
            </xs:element>
        </xs:choice>
    </xs:complexType>

    <xs:attributeGroup name="key-group">
        <xs:attribute name="key" type="xs:string"/>
        <xs:attribute name="escaped-key" type="xs:boolean" use="optional" default="false"/>
    </xs:attributeGroup>

</xs:schema>`

var (
	jsonSchemaOnce sync.Once
	jsonSchema     *Schema
	jsonSchemaErr  error
)

// SchemaForJSON returns the built-in schema for the XML representation of
// JSON, F&O 3.1 §C.2.
//
// The schema is parsed once and shared: it is immutable after assembly, and
// parsing it on every xsl:import-schema would cost more than the whole of the
// json-to-xml test set. The caller must not modify the returned schema —
// mergeSchema in the xslt package copies components out of it rather than
// into it, which is the only use it has.
//
// The error is a programming error rather than a runtime one: the source is
// a constant in this file, so a failure means this package can no longer
// parse a schema it ships with. It is returned rather than panicked because
// the callers are library entry points that already have an error to return.
func SchemaForJSON() (*Schema, error) {
	jsonSchemaOnce.Do(func() {
		tree, err := xdm.ParseString(jsonSchemaSource, xdm.ParseOptions{})
		if err != nil {
			jsonSchemaErr = fmt.Errorf("parsing the built-in schema for JSON: %w", err)
			return
		}
		jsonSchema, err = Load(tree.Root, "", Options{})
		if err != nil {
			jsonSchemaErr = fmt.Errorf("assembling the built-in schema for JSON: %w", err)
		}
	})
	return jsonSchema, jsonSchemaErr
}
