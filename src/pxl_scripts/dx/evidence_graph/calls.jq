(.variables | map({(.name): .defaultValue}) | add) as $d
| .globalFuncs[]
| [.outputName,
   (.func.name + "(" + ([.func.args[] | ($d[.variable] // .value // "") | tojson] | join(", ")) + ")")]
| @tsv
